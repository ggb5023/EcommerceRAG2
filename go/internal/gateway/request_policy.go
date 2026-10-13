package gateway

import (
	"regexp"
	"strings"
	"unicode"
)

// These narrow, conservative preflight rules do not grant access or infer a
// product answer. Protected disclosure and explicitly invalid version reuse
// terminate locally with the existing permission_denied error. Every other
// request still requires the signed scope and live authority checks.
var protectedPrompt = regexp.MustCompile(`系统提示词|系统指令|开发者提示词|systemprompt|systeminstructions|developerprompt`)
var disclosureAction = regexp.MustCompile(`展示|显示|发送|复制|输出|透露|给我|提供|print|show|send|copy|reveal|display`)
var internalMaterial = regexp.MustCompile(`内部资料|内部文档|内部草稿|internaldocument|internalmaterial|internaldraft`)
var customerTarget = regexp.MustCompile(`客户|顾客|对客|customer|client`)
var invalidVersion = regexp.MustCompile(`(版本|资料|文档).*(已|已经|被).*(撤销|撤回|过期|失效)|(revoked|withdrawn|expired)(version|document)`)
var reuseAction = regexp.MustCompile(`继续使用|继续引用|沿用|复用|reuse|stilluse|continuetouse|continueusing`)

func requestPolicyDenied(query string) bool {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, query)
	return protectedPrompt.MatchString(compact) && disclosureAction.MatchString(compact) ||
		internalMaterial.MatchString(compact) && customerTarget.MatchString(compact) && disclosureAction.MatchString(compact) ||
		invalidVersion.MatchString(compact) && reuseAction.MatchString(compact)
}
