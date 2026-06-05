package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type webFetchTool struct {
	client *http.Client
}

type webFetchArgs struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt,omitempty"`
}

// errBlockedAddress marks a dial refused by the SSRF guard so Run can convert
// it into a clean tool-level error instead of an opaque transport failure.
var errBlockedAddress = errors.New("address is not a public host")

// fetchTransport is web_fetch's HTTP transport. It mirrors http.DefaultTransport
// but installs an SSRF guard on every dial: the model (or a page it follows via
// a redirect) cannot steer a fetch at the cloud-metadata endpoint
// (169.254.169.254), a private-network service, or other non-public address.
// The Control hook runs after DNS resolution on the concrete IP about to be
// dialed, so it also defeats DNS-rebinding and redirect-based bypasses. Loopback
// is deliberately allowed so localhost dev servers stay fetchable.
var fetchTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   ssrfSafeControl,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

var (
	httpClient     = &http.Client{Timeout: 15 * time.Second, Transport: fetchTransport}
	schemaWebFetch = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["url"],
  "properties": {
    "url": {"type": "string", "description": "HTTP or HTTPS URL to fetch."},
    "prompt": {"type": "string", "description": "Optional note describing what information is needed from the page."}
  }
}`)
)

//go:embed web_fetch.md
var webFetchDescription string

func newWebFetchTool(Dependencies) Tool {
	return &webFetchTool{client: httpClient}
}

func (t *webFetchTool) Name() string {
	return "web_fetch"
}

func (t *webFetchTool) Description() string {
	return webFetchDescription
}

func (t *webFetchTool) Schema() json.RawMessage {
	return schemaWebFetch
}

func (t *webFetchTool) Run(ctx context.Context, raw json.RawMessage) (res Result, err error) {
	defer recoverTool(ctx, t.Name(), &res, &err)

	var args webFetchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid web_fetch arguments: " + err.Error()), nil
	}
	args.URL = strings.TrimSpace(args.URL)
	if args.URL == "" {
		return errorResult("url is required"), nil
	}
	parsed, err := url.Parse(args.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errorResult("url must be an absolute HTTP or HTTPS URL"), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errorResult("url must use http or https"), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("creating web request: %w", err)
	}
	req.Header.Set("User-Agent", "BharatCode/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		// A dial refused by the SSRF guard (including one reached only via a
		// redirect) is the model's mistake, not an internal failure, so report it
		// as a tool error the model can read and recover from.
		if errors.Is(err, errBlockedAddress) {
			return errorResult("refused to fetch a non-public address (loopback aside, private, link-local, and cloud-metadata hosts are blocked)"), nil
		}
		return Result{}, fmt.Errorf("fetching url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errorResult(fmt.Sprintf("request failed with status %s", resp.Status)), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return Result{}, fmt.Errorf("reading response body: %w", err)
	}
	truncated := len(body) > 2*1024*1024
	if truncated {
		body = body[:2*1024*1024]
	}

	contentType := resp.Header.Get("Content-Type")
	text := string(body)
	if strings.Contains(strings.ToLower(contentType), "html") || strings.Contains(text, "<") {
		text = htmlToMarkdown(text)
	}
	if args.Prompt != "" {
		text = "Fetch note: " + args.Prompt + "\n\n" + text
	}
	if truncated {
		text += "\n\n[truncated response body]"
	}
	return Result{Content: strings.TrimSpace(text)}, nil
}

var (
	reScriptBlock = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reStyleBlock  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reHTMLComment = regexp.MustCompile(`(?is)<!--.*?-->`)
	rePreBlock    = regexp.MustCompile(`(?is)<pre\b[^>]*>(.*?)</pre>`)
	reInlineCode  = regexp.MustCompile(`(?is)<code\b[^>]*>(.*?)</code>`)
	reAnchor      = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	reListItem    = regexp.MustCompile(`(?is)<li\b[^>]*>`)
	reBlockBreak  = regexp.MustCompile(`(?is)</p>|<br\s*/?>|</div>|</section>|</article>|</tr>`)
	reAnyTag      = regexp.MustCompile(`(?is)<[^>]+>`)
)

// ssrfSafeControl is a net.Dialer Control hook that refuses to connect to any
// address the SSRF guard blocks. It receives the already-resolved "ip:port"
// being dialed, so it sees through DNS names and redirects to the concrete IP.
func ssrfSafeControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: unparseable dial address %q", errBlockedAddress, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: unresolved dial host %q", errBlockedAddress, host)
	}
	if isBlockedFetchIP(ip) {
		return fmt.Errorf("%w: %s", errBlockedAddress, ip)
	}
	return nil
}

// isBlockedFetchIP reports whether ip is a non-public address web_fetch must not
// reach. Loopback is allowed so localhost development servers remain fetchable;
// everything else off the public internet — private networks, link-local
// (which carries the 169.254.169.254 cloud-metadata service), multicast, and the
// unspecified address — is refused. The standard net.IP predicates already fold
// in the IPv4-mapped-IPv6 and IPv6 ULA (fc00::/7) cases.
func isBlockedFetchIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return false
	}
	return ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// htmlToMarkdown reduces simple HTML to model-readable, markdown-like text.
// Whitespace is aggressively collapsed everywhere EXCEPT inside <pre> blocks,
// whose indentation and line breaks are preserved verbatim inside fenced code
// blocks — documentation code samples survive intact instead of being flattened
// into a single space-collapsed line.
func htmlToMarkdown(input string) string {
	s := reScriptBlock.ReplaceAllString(input, " ")
	s = reStyleBlock.ReplaceAllString(s, " ")
	s = reHTMLComment.ReplaceAllString(s, " ")

	// Stash each <pre> block as a fenced code block behind a placeholder token
	// that carries no angle brackets or whitespace, so it survives both the tag
	// stripping and field-joining passes below and can be restored verbatim.
	var codeBlocks []string
	s = rePreBlock.ReplaceAllStringFunc(s, func(m string) string {
		inner := rePreBlock.ReplaceAllString(m, "$1")
		inner = reAnyTag.ReplaceAllString(inner, "")
		inner = html.UnescapeString(inner)
		inner = strings.Trim(inner, "\n")
		token := fmt.Sprintf("\x00CODE%d\x00", len(codeBlocks))
		codeBlocks = append(codeBlocks, "\n\n```\n"+inner+"\n```\n\n")
		return "\n\n" + token + "\n\n"
	})

	for i := 6; i >= 1; i-- {
		re := regexp.MustCompile(fmt.Sprintf(`(?is)<h%d\b[^>]*>(.*?)</h%d>`, i, i))
		prefix := strings.Repeat("#", i)
		s = re.ReplaceAllString(s, "\n\n"+prefix+" $1\n\n")
	}
	s = reAnchor.ReplaceAllString(s, "$2 ($1)")
	s = reInlineCode.ReplaceAllString(s, "`$1`")
	s = reListItem.ReplaceAllString(s, "\n- ")
	s = reBlockBreak.ReplaceAllString(s, "\n")
	s = reAnyTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	out := strings.Join(lines, "\n")

	for i, block := range codeBlocks {
		out = strings.Replace(out, fmt.Sprintf("\x00CODE%d\x00", i), block, 1)
	}
	return out
}
