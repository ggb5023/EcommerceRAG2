package gateway

import (
	"encoding/json"
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
var promotionQuestion = regexp.MustCompile(`促销|满减|活动.*名额|promotion`)
var supportedLiveFact = regexp.MustCompile(`库存|现货|有货|价格|报价|多少钱|订单|物流|stock|price|order|logistics`)
var noticeScopeQuestion = regexp.MustCompile(`影响所有店铺|覆盖所有店铺|是否所有店铺|公告.*适用范围|all shops.*affected|scope of.*notice`)

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
		if id != "" || !orderIDPattern.MatchString(candidate) || math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < .5 || confidence > 1 {
			return "", false
		}
		id = candidate
	}
	if id == "" {
		return "", false
	}
	if slices.Contains(queryResourcePattern.FindAllString(query, -1), id) {
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
	var messages []historyMessage
	if json.Unmarshal([]byte(history), &messages) != nil {
		return "", false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		tokens := skuToken.FindAllString(messages[i].Content, -1)
		if len(tokens) == 0 {
			continue
		}
		// Duplicates are not alternative products. Distinct candidates are
		// ambiguous; an assistant's choice cannot resolve that ambiguity.
		unique := map[string]bool{}
		for _, token := range tokens {
			unique[token] = true
		}
		return id, len(unique) == 1 && unique[id]
	}
	return "", false
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
