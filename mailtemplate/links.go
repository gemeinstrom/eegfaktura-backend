package mailtemplate

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Link problem keys, the same in mail-link-cases.json and eegfaktura-billing.
const (
	LinkIp             = "ip"
	LinkShortener      = "shortener"
	LinkTextMismatch   = "text-mismatch"
	LinkTooManyLinks   = "too-many-links"
	LinkTooManyDomains = "too-many-domains"
	LinkPlaceholder    = "placeholder-in-link"
	LinkBadHost        = "bad-host"
)

// LinkProblem is one broken link rule of a mail text, with the message the
// community reads.
type LinkProblem struct {
	Key     string
	Message string
}

// CheckLinks checks the links (<a href>) of a sanitised body against the link
// rules of mail-html-policy.json: no IP address, no URL shortener, a link text
// that shows an address shows the target's, at most maxLinks links and
// maxLinkDomains main domains, no placeholder inside a link (it would show
// another address than the target), a host of ASCII letters, digits, "-" and
// "." only (an umlaut domain in its xn-- form). The fixed footer is not part of
// the body.
// It returns one problem per broken rule, nil when the text is fine.
func CheckLinks(body string) []LinkProblem {
	nodes, err := parseFragment(body)
	if err != nil {
		return nil
	}
	var ips, shorteners, badHosts, mismatches, domains, placeholders []string
	seen := map[string]bool{}
	add := func(list *[]string, kind, v string) {
		if !seen[kind+" "+v] {
			seen[kind+" "+v] = true
			*list = append(*list, v)
		}
	}
	links := 0
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			href := trimSpaces(attr(c, "href"))
			if c.DataAtom != atom.A || href == "" {
				walk(c)
				continue
			}
			links++
			for _, key := range placeholdersIn(c) {
				add(&placeholders, "ph", placeholderLabel(key))
			}
			u, err := url.Parse(href)
			if err != nil {
				continue // the sanitiser keeps parseable URLs only
			}
			linkText := normalizeLinkText(textOf(c))
			if strings.EqualFold(u.Scheme, "mailto") {
				if !textMatchesMailto(linkText, u) {
					add(&mismatches, "text", "„"+linkText+"“")
				}
				continue
			}
			host := strings.TrimSuffix(asciiLower(mapDots(u.Hostname())), ".")
			switch {
			case !strings.HasPrefix(u.Host, "[") && strings.Trim(host, hostChars) != "":
				// fullwidth letters, soft hyphens, zero-width characters, umlauts: a browser
				// maps them to another host than the one shown
				add(&badHosts, "bad", u.Hostname())
			case isIPHost(host):
				add(&ips, "ip", host)
			case isBlockedHost(host):
				add(&shorteners, "short", host)
			}
			add(&domains, "domain", mainDomain(host))
			if !textMatchesHost(linkText, host) {
				add(&mismatches, "text", "„"+linkText+"“")
			}
		}
	}
	walk(wrap(nodes))

	limits := policy.Links
	var out []LinkProblem
	if len(ips) > 0 {
		out = append(out, LinkProblem{LinkIp, "Links zu IP-Adressen sind nicht erlaubt: " + strings.Join(ips, ", ")})
	}
	if len(shorteners) > 0 {
		out = append(out, LinkProblem{LinkShortener, "Links zu Kurz-Link-Diensten sind nicht erlaubt: " + strings.Join(shorteners, ", ")})
	}
	if len(badHosts) > 0 {
		out = append(out, LinkProblem{LinkBadHost, "Die Linkadresse enthält ungültige Zeichen: " + strings.Join(badHosts, ", ")})
	}
	if len(mismatches) > 0 {
		out = append(out, LinkProblem{LinkTextMismatch, "Der Linktext zeigt eine andere Adresse als das Ziel: " + strings.Join(mismatches, ", ")})
	}
	if len(placeholders) > 0 {
		out = append(out, LinkProblem{LinkPlaceholder, "Platzhalter dürfen nicht in einem Link stehen: " + strings.Join(placeholders, ", ")})
	}
	if links > limits.MaxLinks {
		out = append(out, LinkProblem{LinkTooManyLinks, fmt.Sprintf("Höchstens %d Links pro Mail (jetzt %d).", limits.MaxLinks, links)})
	}
	if len(domains) > limits.MaxLinkDomains {
		out = append(out, LinkProblem{LinkTooManyDomains, fmt.Sprintf("Links zu höchstens %d verschiedenen Domains pro Mail (jetzt %d: %s).",
			limits.MaxLinkDomains, len(domains), strings.Join(domains, ", "))})
	}
	return out
}

// LinkErrors returns the link problems of a sanitised body as validation errors of bodyHtml.
func LinkErrors(body string) []*ValidationError {
	var out []*ValidationError
	for _, p := range CheckLinks(body) {
		out = append(out, &ValidationError{Field: "bodyHtml", Message: p.Message})
	}
	return out
}

// isIPHost reports whether a host (lower case, dots mapped) is an IP address:
// IPv6 (only in brackets, without them here), or labels that are all numbers in
// decimal or 0x hex (1.2.3.4, 3232235777, 0x7f.1). A single hex word like
// "cafe" is a name.
func isIPHost(host string) bool {
	if strings.Contains(host, ":") {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if !isNumber(label) {
			return false
		}
	}
	return true
}

func isNumber(s string) bool {
	if hex, ok := strings.CutPrefix(s, "0x"); ok {
		return strings.Trim(hex, "0123456789abcdef") == ""
	}
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// hostChars are the characters of a host name (lower case, dots mapped).
const hostChars = "abcdefghijklmnopqrstuvwxyz0123456789-."

// asciiLower lower-cases A-Z only, as eegfaktura-billing does (no Unicode case
// mapping, which differs between Go and Java, e.g. for İ).
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// mapDots replaces the dots browsers accept in a host (。 ． ｡) by ".".
var mapDots = strings.NewReplacer("\u3002", ".", "\uff0e", ".", "\uff61", ".").Replace

// isBlockedHost reports whether a host is a URL shortener of the policy or one of its subdomains.
func isBlockedHost(host string) bool {
	for _, b := range policy.Links.BlockedHosts {
		if host == b || strings.HasSuffix(host, "."+b) {
			return true
		}
	}
	return false
}

// domainOf is the host a link text is compared with: lower case, dots mapped, without a leading www.
func domainOf(host string) string {
	return strings.TrimPrefix(strings.TrimSuffix(asciiLower(mapDots(host)), "."), "www.")
}

// mainDomain is the domain a link counts for: the last two labels of the host,
// the last three under a second-level suffix of the policy (e.g. co.at). An IP
// address counts as itself.
func mainDomain(host string) string {
	host = domainOf(host)
	if isIPHost(host) {
		return host
	}
	labels := strings.Split(host, ".")
	n := 2
	if len(labels) >= 3 {
		suffix := strings.Join(labels[len(labels)-2:], ".")
		for _, s := range policy.Links.SecondLevelSuffixes {
			if suffix == s {
				n = 3
			}
		}
	}
	if len(labels) <= n {
		return host
	}
	return strings.Join(labels[len(labels)-n:], ".")
}

// isFileName reports whether a host-like text ends in a file extension of the policy (anleitung.pdf).
func isFileName(host string) bool {
	labels := strings.Split(asciiLower(host), ".")
	last := labels[len(labels)-1]
	for _, ext := range policy.Links.FileExtensions {
		if last == ext {
			return true
		}
	}
	return false
}

// The address extraction of a link text is the same algorithm as in
// eegfaktura-billing (MailLinkRules): the text is split into words, and every
// word that is an address must show the link's own address.
var (
	// formatChars are invisible: soft hyphen, zero-width characters, BOM, ...
	formatChars = regexp.MustCompile(`[\p{Cf}\x{034F}\x{180E}]`)
	textSpace   = regexp.MustCompile(`[\p{Z}\t\n\v\f\r]+`)
	// mailAddress is a word like office@example.at.
	mailAddress = regexp.MustCompile(`^[^@\s]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
	// bareDomain is a word like example.at or example.at/pfad.
	bareDomain = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}([/?#]\S*)?$`)
)

// normalizeLinkText removes invisible characters, maps fullwidth ASCII (ｂａｎｋ)
// and the dot variants to ASCII and turns every Unicode space into one plain
// space.
func normalizeLinkText(text string) string {
	text = strings.Map(func(r rune) rune {
		switch {
		case r >= 0xff01 && r <= 0xff5e:
			return r - 0xfee0
		case r == 0x3000:
			return ' '
		}
		return r
	}, formatChars.ReplaceAllString(text, ""))
	text = textSpace.ReplaceAllString(mapDots(text), " ")
	return strings.Trim(text, " ")
}

// linkWords splits a normalised link text into words: at spaces, and at a ":"
// that is not followed by "//" (Website:bank.at, mailto:office@eeg.at).
func linkWords(text string) []string {
	var words []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' || text[i] == ':' && !strings.HasPrefix(text[i+1:], "//") {
			words = append(words, text[start:i])
			start = i + 1
		}
	}
	return append(words, text[start:])
}

// stripWord removes punctuation and symbols (quotes, », …, *, →) from both
// ends of a word; a final "/" stays (eeg.at/).
func stripWord(w string) string {
	w = strings.TrimLeftFunc(w, isPunctOrSymbol)
	for w != "" && !strings.HasSuffix(w, "/") {
		r, size := utf8.DecodeLastRuneInString(w)
		if !isPunctOrSymbol(r) {
			break
		}
		w = w[:len(w)-size]
	}
	return w
}

func isPunctOrSymbol(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// shownAddress is an address a link text shows: a host (web) or an e-mail address, lower case.
type shownAddress struct {
	mail  bool
	value string
}

// shownAddresses returns the addresses in a normalised link text.
func shownAddresses(text string) []shownAddress {
	var out []shownAddress
	for _, w := range linkWords(text) {
		w = stripWord(w)
		lower := asciiLower(w)
		switch {
		case w == "":
		case strings.Contains(w, "://"):
			authority := upToPathEnd(w[strings.Index(w, "://")+3:])
			out = append(out, shownAddress{value: domainOf(hostOfAuthority(authority))})
		case strings.HasPrefix(lower, "www."):
			out = append(out, shownAddress{value: domainOf(upToPathEnd(w))})
		case mailAddress.MatchString(lower):
			out = append(out, shownAddress{mail: true, value: lower})
		case bareDomain.MatchString(w) && !isFileName(upToPathEnd(w)):
			out = append(out, shownAddress{value: domainOf(upToPathEnd(w))})
		}
	}
	return out
}

// upToPathEnd is s up to the first / ? or #.
func upToPathEnd(s string) string {
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		return s[:i]
	}
	return s
}

// hostOfAuthority is the host of an authority: without user info (up to the last @) and port.
func hostOfAuthority(authority string) string {
	authority = authority[strings.LastIndex(authority, "@")+1:]
	if strings.HasPrefix(authority, "[") {
		if end := strings.Index(authority, "]"); end >= 0 {
			return authority[:end+1]
		}
		return authority
	}
	host, _, _ := strings.Cut(authority, ":")
	return host
}

// textMatchesHost: every web address a link text shows is the link's host; an
// e-mail address never matches a web link.
func textMatchesHost(text, host string) bool {
	for _, a := range shownAddresses(text) {
		if a.mail || a.value != domainOf(host) {
			return false
		}
	}
	return true
}

// textMatchesMailto: every e-mail address a link text shows is the link's
// address (percent-decoded); a web address never matches a mailto link.
func textMatchesMailto(text string, u *url.URL) bool {
	addr, _, _ := strings.Cut(u.Opaque, "?")
	if a, err := url.PathUnescape(addr); err == nil {
		addr = a
	}
	addr = asciiLower(trimSpaces(addr))
	for _, a := range shownAddresses(text) {
		if !a.mail || a.value != addr {
			return false
		}
	}
	return true
}

// placeholdersIn returns the placeholder keys (data-ph) inside n.
func placeholdersIn(n *html.Node) []string {
	var keys []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if key := attr(c, "data-ph"); strings.TrimSpace(key) != "" {
			keys = append(keys, key)
			continue
		}
		keys = append(keys, placeholdersIn(c)...)
	}
	return keys
}

// placeholderLabel is the label of a placeholder key in the catalog, the key if no mail knows it.
func placeholderLabel(key string) string {
	for i := range catalog {
		if ph := catalog[i].Placeholder(key); ph != nil {
			return ph.Label
		}
	}
	return key
}

// textOf returns the text content of n without the labels of placeholders
// (a placeholder in a link is a problem of its own, see placeholdersIn), with
// the alt text of images (shown when images are blocked).
func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			}
			if c.Type == html.ElementNode && c.DataAtom == atom.Img && hasAttr(c, "alt") {
				// an image without alt adds nothing, as in eegfaktura-billing
				b.WriteString(" " + attr(c, "alt") + " ")
			}
			if c.Type == html.ElementNode && strings.TrimSpace(attr(c, "data-ph")) != "" {
				continue
			}
			rec(c)
		}
	}
	rec(n)
	return b.String()
}
