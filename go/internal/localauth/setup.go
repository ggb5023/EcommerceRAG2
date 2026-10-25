package localauth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Setup serves the one-time first-run page. It writes only local_credential:
// the owner skeleton and its grants come from the bootstrap job.
type Setup struct {
	Store       *Store
	Token       string
	TokenIssued time.Time
	mu          sync.Mutex
}

// SetupTokenTTL bounds the printed install token. A restart prints a new one.
const SetupTokenTTL = 30 * time.Minute
const setupCookie = "ecr_setup_token"

type gate int

const (
	gateDenied gate = iota
	gateAllowed
	gateExpired
)

func (s *Setup) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Query().Get("token") != "" {
		if s.authorized(r) == gateAllowed {
			s.setTokenCookie(w, r, r.URL.Query().Get("token"))
			w.Header().Set("Referrer-Policy", "no-referrer")
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
	}
	token := s.requestToken(r)
	switch s.authorized(r) {
	case gateExpired:
		s.render(w, http.StatusConflict, "安装令牌已过期",
			"一次性安装令牌只有 30 分钟有效期。请重启 gateway 生成新令牌后重试。", "", "", "", token)
		return
	case gateDenied:
		s.render(w, http.StatusForbidden, "安装入口已锁定",
			"只允许从 loopback 访问，或携带启动日志中的一次性安装令牌。", "", "", "", token)
		return
	}
	target, err := s.Store.SetupTarget(r.Context())
	switch {
	case errors.Is(err, ErrSetupComplete):
		s.render(w, http.StatusConflict, "安装已完成",
			"本地账号已存在。重置密码请使用运维命令。",
			"go run ./cmd/local-account reset-password --env <0600 运行配置> --user <用户名>", "", "", token)
		return
	case errors.Is(err, ErrSetupNeedsBootstrap):
		s.render(w, http.StatusConflict, "还缺少初始化骨架",
			"未找到唯一的 owner 骨架或 merchant 租户。请先运行一次性 bootstrap，再回到本页设置密码。",
			"docker compose up -d  # db-init → migrate → bootstrap 会自动执行", "docs/install.md", "", token)
		return
	case err != nil:
		s.render(w, http.StatusServiceUnavailable, "数据库暂不可用", "请稍后重试。", "", "", "", token)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			s.render(w, http.StatusBadRequest, "请求无法解析", "请重新提交。", "", "", target.OwnerExternalID, token)
			return
		}
		password := r.PostForm.Get("password")
		if len(password) < 12 || len(password) > 256 {
			s.render(w, http.StatusBadRequest, "密码长度不符合要求", "密码需要 12 到 256 个字节。", "", "", target.OwnerExternalID, token)
			return
		}
		hash, err := Hash(password)
		if err != nil {
			s.render(w, http.StatusServiceUnavailable, "无法生成密码哈希", "请稍后重试。", "", "", target.OwnerExternalID, token)
			return
		}
		if err := s.Store.CompleteSetup(r.Context(), target, hash); err != nil {
			if errors.Is(err, ErrSetupComplete) {
				s.render(w, http.StatusConflict, "安装已完成", "本地账号已存在。", "", "", "", token)
				return
			}
			s.render(w, http.StatusServiceUnavailable, "安装未完成", "请检查数据库后重试。", "", "", "", token)
			return
		}
		s.mu.Lock()
		s.Token = ""
		s.mu.Unlock()
		s.clearTokenCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "首次安装",
		"账号已由 bootstrap 创建。请为下面的 owner 账号设置密码；设置完成后此页面永久关闭。",
		"", "", target.OwnerExternalID, token)
}

func (s *Setup) requestToken(r *http.Request) string {
	if token := r.Header.Get("X-Setup-Token"); token != "" {
		return token
	}
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	if cookie, err := r.Cookie(setupCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		return r.PostForm.Get("token")
	}
	return ""
}

func (s *Setup) authorized(r *http.Request) gate {
	token := s.requestToken(r)
	s.mu.Lock()
	expected, issued := s.Token, s.TokenIssued
	s.mu.Unlock()
	if expected != "" && len(token) == len(expected) &&
		subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1 {
		if issued.IsZero() || time.Since(issued) <= SetupTokenTTL {
			return gateAllowed
		}
		return gateExpired
	}
	// Loopback免令牌只在开发档：开发拓扑里 Vite 会把 /setup 代理到 127.0.0.1，
	// 网关看到的都是 loopback；只要出现任何转发头就等同于非本机。
	if os.Getenv("APP_ENV") != "development" {
		return gateDenied
	}
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if r.Header.Get(header) != "" {
			return gateDenied
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return gateDenied
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return gateAllowed
	}
	return gateDenied
}

func (s *Setup) setTokenCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: token, Path: "/setup", HttpOnly: true,
		Secure: setupSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(SetupTokenTTL.Seconds())})
}

func (s *Setup) clearTokenCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: "", Path: "/setup", HttpOnly: true,
		Secure: setupSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func setupSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Setup) render(w http.ResponseWriter, status int, title, message, command, doc, username, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	guidance := ""
	if command != "" {
		guidance += "<p>请执行：</p><pre><code>" + html.EscapeString(command) + "</code></pre>"
	}
	if doc != "" {
		guidance += "<p>说明见 <code>" + html.EscapeString(doc) + "</code>。</p>"
	}
	form := ""
	if username != "" {
		form = fmt.Sprintf(`<form method="post"><input type="hidden" name="token" value="%s"><label>账号</label>
<input value="%s" readonly><label>新密码（12–256 字节）</label>
<input type="password" name="password" required minlength="12" maxlength="256">
<button type="submit">设置密码并启用账号</button></form>`, html.EscapeString(token), html.EscapeString(username))
	}
	fmt.Fprintf(w, page, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message), guidance, form)
}

const page = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title>
<style>body{margin:0;min-width:320px;font:14px/1.6 Inter,system-ui,sans-serif;background:#f6f8f6;color:#17231f}
main{max-width:560px;margin:12vh auto;padding:28px;background:#fff;border:1px solid #e2e9e4;border-radius:10px}
h1{font-size:20px;margin:0 0 12px}p{color:#52635b}code,pre{background:#f2f6f3;border-radius:6px}
pre{padding:12px;overflow:auto}button{margin-top:18px;padding:10px 16px;border:0;border-radius:6px;background:#1b654d;color:#fff;font-weight:700}
input{display:block;width:100%%;margin:6px 0 12px;padding:10px;border:1px solid #d8e2dc;border-radius:6px;font:inherit}
label{color:#52635b;font-size:12px}</style></head><body><main><h1>%s</h1><p>%s</p>%s%s</main></body></html>`
