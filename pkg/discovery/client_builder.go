package discovery

import (
	"net/url"
	"net/http"
	"net"
	"strings"
	"fmt"

	"github.com/directxman12/scrape-goat/pkg/config"
)

// reqToCurlCommand rreturns the curl command to execute the given request
// (more or less).
func reqToCurlCommand(r *http.Request) string {
	headers := ""
	for key, vals := range r.Header {
		for _, val := range vals {
			headers += fmt.Sprintf(" -H %q", fmt.Sprintf("%s: %s", key, val))
		}
	}
	return fmt.Sprintf("curl -v -X%s %s %s", r.Method, headers, r.URL.String())
}

// ReqBuilder knows how to construct scrape HTTP requests for scrape targets.
type ReqBuilder interface {
	// RequestFor constructs a URL for the given host on the given scrape target.
	RequestFor(host string, target ScrapeTarget) (*http.Request, error)
}

type builderFunc func(ScrapeTarget) string

// reqBuilder constructs URLs for a scrape target based
// on some configuration.
type reqBuilder struct {
	pathFunc builderFunc
	portFunc builderFunc
	schemeFunc builderFunc
	userFunc builderFunc
	passFunc builderFunc
}

func (b *reqBuilder) RequestFor(host string, target ScrapeTarget) (*http.Request, error) {
	loc := &url.URL{
		// TODO: validate these?
		Host: net.JoinHostPort(host, b.portFunc(target)),
		Path: b.pathFunc(target),
		Scheme: b.schemeFunc(target),
	}

	req, err := http.NewRequest("GET", loc.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("unable to construct request for host %q on target %q: %v", host, target.Key(), err)
	}

	user := b.userFunc(target)
	pass := b.passFunc(target)
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}

	return req, nil
}

func constSource(val string) builderFunc {
	return func(_ ScrapeTarget) string {
		return val
	}
}

func metaSource(metaPath string) builderFunc {
	parts := strings.Split(metaPath, ":")
	return func(target ScrapeTarget) string {
		return target.SourceValue(parts...)
	}
}

func sourceFor(cfg *config.VariableSource) (builderFunc, error) {
	if cfg == nil {
		return constSource(""), nil
	}

	if cfg.From != "" && cfg.Const != "" {
		return nil, fmt.Errorf("cannot specify more than one source of information per part")
	}

	if cfg.From != "" {
		return metaSource(cfg.From), nil
	}

	// fall back to const source, even if it's empty
	return constSource(cfg.Const), nil
}

// NewReqBuilderFromConfig constructs a new URL builder capable of fetching metadata
// from scrape targets, using the given configuration.
func NewReqBuilderFromConfig(cfg *config.ConnectionConfig) (ReqBuilder, error) {
	pathFunc, err := sourceFor(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("could not construct source for path: %v", err)
	}
	portFunc, err := sourceFor(cfg.Port)
	if err != nil {
		return nil, fmt.Errorf("could not construct source for port: %v", err)
	}
	schemeFunc, err := sourceFor(cfg.Scheme)
	if err != nil {
		return nil, fmt.Errorf("could not construct source for scheme: %v", err)
	}
	userFunc, err := sourceFor(cfg.Username)
	if err != nil {
		return nil, fmt.Errorf("could not construct source for user: %v", err)
	}
	passFunc, err := sourceFor(cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("could not construct source for password: %v", err)
	}
	return &reqBuilder{
		pathFunc: pathFunc,
		portFunc: portFunc,
		schemeFunc: schemeFunc,
		userFunc: userFunc,
		passFunc: passFunc,
	}, nil
}
