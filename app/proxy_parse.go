package main

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/8bu/ladilala/core/profile"
)

func validProxyScheme(s profile.ProxyScheme) bool {
	return s == profile.ProxyHTTP || s == profile.ProxyHTTPS || s == profile.ProxySOCKS5
}

// validProxyHost accepts a DNS name, an IPv4 address or an IPv6 address
// without brackets.
func validProxyHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
		default:
			return false
		}
	}
	return !strings.HasPrefix(h, ".") && !strings.HasSuffix(h, ".")
}

func parsePort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= 65535
}

// parseProxyLine reads one import line. The reason is empty on success, or
// one of format, scheme, host, port.
func parseProxyLine(line string, def profile.ProxyScheme) (pp profile.Proxy, reason string) {
	s := strings.TrimSpace(line)
	pp.Scheme = def
	if i := strings.Index(s, "://"); i >= 0 {
		pp.Scheme = profile.ProxyScheme(strings.ToLower(s[:i]))
		s = s[i+3:]
		if !validProxyScheme(pp.Scheme) {
			return pp, "scheme"
		}
	}
	s = strings.TrimSuffix(s, "/")
	var host, port string
	bracket := false
	if at := strings.LastIndex(s, "@"); at >= 0 {
		cred, hp := s[:at], s[at+1:]
		u, p, _ := strings.Cut(cred, ":")
		var err error
		if pp.Username, err = url.PathUnescape(u); err != nil {
			return pp, "format"
		}
		if pp.Password, err = url.PathUnescape(p); err != nil {
			return pp, "format"
		}
		host, port, bracket, reason = splitHostPort(hp)
		if reason != "" {
			return pp, reason
		}
	} else if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 || len(s) <= end+1 || s[end+1] != ':' {
			return pp, "format"
		}
		host, bracket = s[1:end], true
		parts := strings.SplitN(s[end+2:], ":", 3)
		if len(parts) == 2 {
			return pp, "format"
		}
		port = parts[0]
		if len(parts) == 3 {
			pp.Username, pp.Password = parts[1], parts[2]
		}
	} else {
		parts := strings.SplitN(s, ":", 4)
		switch len(parts) {
		case 2:
			host, port = parts[0], parts[1]
		case 4:
			host, port, pp.Username, pp.Password = parts[0], parts[1], parts[2], parts[3]
		default:
			return pp, "format"
		}
	}
	if !validProxyHost(host) {
		return pp, "host"
	}
	if bracket || strings.Contains(host, ":") {
		if a, err := netip.ParseAddr(host); err != nil || !a.Is6() {
			return pp, "host"
		}
	}
	n, ok := parsePort(port)
	if !ok {
		return pp, "port"
	}
	pp.Host, pp.Port = host, n
	return pp, ""
}

// splitHostPort splits host:port or [ipv6]:port.
func splitHostPort(hp string) (host, port string, bracket bool, reason string) {
	if strings.HasPrefix(hp, "[") {
		end := strings.Index(hp, "]")
		if end < 0 || len(hp) <= end+1 || hp[end+1] != ':' {
			return "", "", false, "format"
		}
		return hp[1:end], hp[end+2:], true, ""
	}
	i := strings.LastIndex(hp, ":")
	if i < 0 || strings.Count(hp, ":") > 1 {
		return "", "", false, "format"
	}
	return hp[:i], hp[i+1:], false, ""
}
