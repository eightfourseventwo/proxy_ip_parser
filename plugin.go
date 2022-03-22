package proxy_ip_parser

import (
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/roadrunner-server/api/v2/plugins/config"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const (
	name      string = "proxy_ip_parser"
	configKey string = "http.trusted_subnets"
	xff       string = "X-Forwarded-For"
	xrip      string = "X-Real-Ip"
	tcip      string = "True-Client-Ip"
	cfip      string = "Cf-Connecting-Ip"
	forwarded string = "Forwarded"
)

var (
	forwardedRegex = regexp.MustCompile(`(?i)(?:for=)([^(;|,| )]+)`)
)

type Plugin struct {
	cfg        *Config
	log        *zap.Logger
	trustedMap map[string]bool
}

func (p *Plugin) Init(cfg config.Configurer, l *zap.Logger) error {
	const op = errors.Op("proxy_ip_parser_init")

	if !cfg.Has(configKey) {
		return errors.E(errors.Disabled)
	}

	err := cfg.UnmarshalKey(configKey, &p.cfg)
	if err != nil {
		return errors.E(op, err)
	}

	if len(p.cfg.TrustedSubnets) == 0 {
		return errors.E(errors.Disabled)
	}

	err = p.cfg.InitDefaults()
	if err != nil {
		return err
	}

	p.log = &zap.Logger{}
	*p.log = *l

	p.trustedMap = make(map[string]bool, len(p.cfg.TrustedSubnets))
	for i := 0; i < len(p.cfg.TrustedSubnets); i++ {
		ip, ipNet, err := net.ParseCIDR(p.cfg.TrustedSubnets[i])
		if err != nil {
			return errors.E(op, err)
		}

		for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); inc(ip) {
			p.trustedMap[ip.String()] = true
		}
	}

	return nil
}

func (p *Plugin) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := net.ParseIP(r.RemoteAddr)
		if ok := p.trustedMap[ip.String()]; ok {
			resolvedIp := p.resolveIP(r.Header)
			if resolvedIp != "" {
				r.RemoteAddr = resolvedIp
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) Name() string {
	return name
}

// get real ip passing multiple proxy
func (p *Plugin) resolveIP(headers http.Header) string {
	if fwd := headers.Get(xff); fwd != "" {
		s := strings.Index(fwd, ", ")
		if s == -1 {
			return fwd
		}

		if len(fwd) < s {
			return ""
		}

		return fwd[:s]
		// next -> X-Real-Ip
	} else if fwd := headers.Get(xrip); fwd != "" {
		return fwd
		// new Forwarded header
		//https://datatracker.ietf.org/doc/html/rfc7239
	} else if fwd := headers.Get(forwarded); fwd != "" {
		if get := forwardedRegex.FindStringSubmatch(fwd); len(get) > 1 {
			// IPv6 -> It is important to note that an IPv6 address and any nodename with
			// node-port specified MUST be quoted
			// we should trim the "
			return strings.Trim(get[1], `"`)
		}
	}

	// The logic here is the following:
	// CloudFlare headers
	// True-Client-IP is a general CF header in which copied information from X-Real-Ip in CF.
	// CF-Connecting-IP is an Enterprise feature and we check it last in order.
	// This operations are near O(1) because Headers struct are the map type -> type MIMEHeader map[string][]string
	if fwd := headers.Get(tcip); fwd != "" {
		return fwd
	}

	if fwd := headers.Get(cfip); fwd != "" {
		return fwd
	}

	return ""
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
