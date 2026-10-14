package admin

import (
	"net"
	"net/url"
	"regexp"
)

var customerSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func customerChatURL(origin, slug string, enabled bool) string {
	if !enabled || !customerSlug.MatchString(slug) {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return ""
	}
	u.Path = "/shop/" + slug
	return u.String()
}
