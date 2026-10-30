package gateway

import (
	"encoding/json"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	"math"
	"regexp"
	"slices"
	"strings"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

type historyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var workflowSteps = regexp.MustCompile(`(应该|需要|该|如何|怎么).*(查什么|核对什么|确认什么|查询哪些|查哪些|处理|澄清)|(what|which).*(check|verify).*(information|before)|how.*(handle|clarify)`)
var catalogRecommendation = regexp.MustCompile(`有没有.*适合|recommend.*for`)
var liveCatalogFact = regexp.MustCompile(`现在|当前|今天|库存|现货|有货|价格|报价|多少钱|current|today|stock|price|available`)
var productReference = regexp.MustCompile(`刚才|上一轮|之前.*商品|那件|同一.*商品|that product|same product|previous product`)
var skuToken = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*[-_][A-Za-z0-9_-]+`)
var skuIdentifierToken = regexp.MustCompile(`(?i)\bSKU[-_][A-Za-z0-9][A-Za-z0-9_-]*\b`)
var productContext = regexp.MustCompile(`(?i)商品|产品|SKU|耳机|杯子|商品款|product|item`)
var standardReference = regexp.MustCompile(`沿用.{0,8}(上一问|上一轮).{0,8}标准|沿用上一问的标准|沿用上一轮的标准|刚才.{0,8}标准`)
var standardToken = regexp.MustCompile(`(?i)(USB[- ]C|USB[- ]?[0-9]+(?:\.[0-9]+)*|IEEE[- ]?[0-9]+(?:\.[0-9]+)*|ISO[- ]?[0-9]+(?:[-:.][0-9]+)*|IEC[- ]?[0-9]+(?:[-:.][0-9]+)*)`)
var promotionQuestion = regexp.MustCompile(`促销|满减|活动.*名额|promotion`)
var supportedLiveFact = regexp.MustCompile(`库存|现货|有货|价格|报价|多少钱|订单|物流|stock|price|order|logistics`)
var noticeScopeQuestion = regexp.MustCompile(`影响所有店铺|覆盖所有店铺|是否所有店铺|公告.*适用范围|all shops.*affected|scope of.*notice`)

// selectedHistoryResource only trusts the nearest prior user message that names
// a resource. Assistant suggestions are never a selection or authorization.
func selectedHistoryResource(history string, pattern *regexp.Regexp) (string, bool) {
	var messages []historyMessage
	if json.Unmarshal([]byte(history), &messages) != nil {
		return "", false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		matches := pattern.FindAllString(messages[i].Content, -1)
		if len(matches) == 0 {
			if pattern == skuIdentifierToken && productContext.MatchString(messages[i].Content) {
				return "", false
			}
			continue
		}
		unique := make(map[string]struct{}, len(matches))
		for _, match := range matches {
			unique[match] = struct{}{}
		}
		if len(unique) != 1 {
			return "", false
		}
		for value := range unique {
			return value, true
		}
	}
	return "", false
}

// resolveHistoryReference repairs only an explicit, unique reference to the
// immediately available user history. It never treats history as current facts.
func resolveHistoryReference(response *ragv1.UnderstandResponse, query, history string) {
	if response == nil {
		return
	}
	lower := strings.ToLower(query)
	if productReference.MatchString(lower) && liveCatalogFact.MatchString(lower) {
		if sku, ok := selectedHistoryResource(history, skuIdentifierToken); ok {
			response.Intent = "factual"
			response.InformationSource = "realtime_business"
			response.RequiredFactTools = nil
			if regexp.MustCompile(`库存|现货|有货|stock|available`).MatchString(lower) {
				response.RequiredFactTools = append(response.RequiredFactTools, "get_stock")
			}
			if regexp.MustCompile(`价格|报价|多少钱|price`).MatchString(lower) {
				response.RequiredFactTools = append(response.RequiredFactTools, "get_price")
			}
			response.Entities = []*ragv1.EntitySlot{{Name: "sku", Value: sku, Confidence: 1}}
			response.Clarification = ""
			response.RewrittenQuery = query
		}
		return
	}
	if !standardReference.MatchString(query) || standardToken.MatchString(query) {
		return
	}
	var messages []historyMessage
	if json.Unmarshal([]byte(history), &messages) != nil {
		return
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" || !strings.Contains(messages[i].Content, "标准") {
			continue
		}
		standard, ok := selectedHistoryResourceForMessage(messages[i].Content, standardToken)
		if !ok {
			return
		}
		response.Intent = "knowledge"
		response.InformationSource = "knowledge"
		response.RequiredFactTools = nil
		response.Entities = nil
		response.Clarification = ""
		response.RewrittenQuery = standard + " 标准最新版本及发布日期；" + query
		return
	}
}

func selectedHistoryResourceForMessage(content string, pattern *regexp.Regexp) (string, bool) {
	matches := pattern.FindAllString(content, -1)
	unique := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		unique[match] = struct{}{}
	}
	if len(unique) != 1 {
		return "", false
	}
	for value := range unique {
		return value, true
	}
	return "", false
}

func routeKnowledgeQuestion(response *ragv1.UnderstandResponse, query string) {
	lower := strings.ToLower(query)
	workflow := workflowSteps.MatchString(lower)
	recommendation := catalogRecommendation.MatchString(lower) && !liveCatalogFact.MatchString(lower)
	// Promotion has no live fact tool. Retrieve available snapshots and their
	// limits before asking for missing details; an activity ID cannot unlock a
	// nonexistent API. Mixed supported fact requests retain resource checks.
	promotion := promotionQuestion.MatchString(lower) && !supportedLiveFact.MatchString(lower)
	noticeScope := noticeScopeQuestion.MatchString(lower) && len(skuToken.FindAllString(query, -1)) == 0
	if !workflow && !recommendation && !promotion && !noticeScope {
		return
	}
	// A request to describe a procedure does not authorize executing that
	// procedure. Catalog suitability can be retrieved before a user selects a
	// SKU; explicit stock/price requests still require resource authorization.
	response.Intent = "knowledge"
	response.InformationSource = "knowledge"
	response.RequiredFactTools = nil
	response.Entities = nil
	response.Clarification = ""
	response.RewrittenQuery = query
}

func routedProductResource(response *ragv1.UnderstandResponse, query, history string) (string, bool) {
	var id string
	for _, entity := range response.Entities {
		if entity.GetName() != "sku" {
			continue
		}
		confidence := entity.GetConfidence()
		candidate := strings.TrimSpace(entity.GetValue())
		if id != "" || !domain.IsOrderIdentifier(candidate) || math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < .5 || confidence > 1 {
			return "", false
		}
		id = candidate
	}
	if id == "" {
		return "", false
	}
	if slices.Contains(domain.ResourceTokens(query), id) {
		current := map[string]bool{}
		for _, token := range skuToken.FindAllString(query, -1) {
			current[token] = true
		}
		if len(current) > 1 {
			return "", false
		}
		return id, true
	}
	if !productReference.MatchString(strings.ToLower(query)) || len(skuToken.FindAllString(query, -1)) > 0 {
		return "", false
	}
	selected, ok := selectedHistoryResource(history, skuIdentifierToken)
	return id, ok && selected == id
}

// Drop whole older messages rather than truncating an identifier or selecting
// part of a multi-candidate reference. Oversized history fails closed.
func boundedHistory(messages []historyMessage) string {
	parts := make([]historyMessage, 0, 6)
	for _, message := range messages {
		if (message.Role == "user" || message.Role == "assistant") && strings.TrimSpace(message.Content) != "" {
			parts = append(parts, message)
		}
	}
	if len(parts) > 6 {
		parts = parts[len(parts)-6:]
	}
	for len(parts) > 0 {
		encoded, err := json.Marshal(parts)
		if err != nil {
			return ""
		}
		if len(encoded) <= 8192 {
			return string(encoded)
		}
		parts = parts[1:]
	}
	return ""
}
