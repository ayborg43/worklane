package wiki

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// RenderMarkdown converts a small, deliberately limited markdown subset
// (#/##/### headers, - lists, **bold**, *italic*, `code`, [text](url)
// links, and [[Page Title]] cross-links to other wiki pages in the same
// project) into safe HTML.
//
// Security: the ENTIRE input is html-escaped first, before any markup
// syntax is recognized — every literal "<", ">", "&", "\"" a user typed
// becomes inert text at that point. Every regex below matches against
// already-escaped text and only ever wraps it in a small, fixed set of
// tags this function itself writes, so there is no way for input text to
// introduce a new tag or attribute. The one place raw text becomes part
// of an attribute (link href) is separately scheme-validated by isSafeURL
// before use, rejecting anything but http(s)/mailto/relative/anchor
// links — in particular "javascript:" and "data:" URIs.
func RenderMarkdown(src string, titleIndex map[string]int64, projectID int64) template.HTML {
	escaped := html.EscapeString(src)
	lines := strings.Split(escaped, "\n")

	var blocks []string
	var para []string
	var list []string

	flushPara := func() {
		if len(para) > 0 {
			blocks = append(blocks, "<p>"+strings.Join(para, "<br>")+"</p>")
			para = nil
		}
	}
	flushList := func() {
		if len(list) > 0 {
			blocks = append(blocks, "<ul>"+strings.Join(list, "")+"</ul>")
			list = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flushPara()
			flushList()
		case strings.HasPrefix(trimmed, "### "):
			flushPara()
			flushList()
			blocks = append(blocks, "<h3>"+strings.TrimPrefix(trimmed, "### ")+"</h3>")
		case strings.HasPrefix(trimmed, "## "):
			flushPara()
			flushList()
			blocks = append(blocks, "<h2>"+strings.TrimPrefix(trimmed, "## ")+"</h2>")
		case strings.HasPrefix(trimmed, "# "):
			flushPara()
			flushList()
			blocks = append(blocks, "<h1>"+strings.TrimPrefix(trimmed, "# ")+"</h1>")
		case strings.HasPrefix(trimmed, "- "):
			flushPara()
			list = append(list, "<li>"+strings.TrimPrefix(trimmed, "- ")+"</li>")
		default:
			flushList()
			para = append(para, trimmed)
		}
	}
	flushPara()
	flushList()

	out := strings.Join(blocks, "\n")
	out = reWikiLink.ReplaceAllStringFunc(out, func(m string) string {
		title := reWikiLink.FindStringSubmatch(m)[1]
		return renderWikiLink(title, titleIndex, projectID)
	})
	out = reLink.ReplaceAllStringFunc(out, func(m string) string {
		sub := reLink.FindStringSubmatch(m)
		text, url := sub[1], sub[2]
		if !isSafeURL(url) {
			return text
		}
		return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener">%s</a>`, url, text)
	})
	out = reBold.ReplaceAllString(out, `<strong>$1</strong>`)
	out = reItalic.ReplaceAllString(out, `<em>$1</em>`)
	out = reCode.ReplaceAllString(out, `<code>$1</code>`)

	return template.HTML(out)
}

var (
	reBold     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic   = regexp.MustCompile(`\*(.+?)\*`)
	reCode     = regexp.MustCompile("`([^`]+?)`")
	reLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reWikiLink = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
)

// isSafeURL allow-lists link schemes — anything else (javascript:, data:,
// vbscript:, ...) is rejected rather than attempted to block by name, so
// an unanticipated scheme fails closed instead of open.
func isSafeURL(u string) bool {
	u = strings.TrimSpace(u)
	lower := strings.ToLower(u)
	switch {
	case strings.HasPrefix(lower, "http://"),
		strings.HasPrefix(lower, "https://"),
		strings.HasPrefix(lower, "mailto:"),
		strings.HasPrefix(u, "/"),
		strings.HasPrefix(u, "#"):
		return true
	default:
		return false
	}
}

// renderWikiLink resolves a [[Page Title]] reference against this
// project's other pages (case-insensitive exact title match). Unresolved
// references render as plain text rather than a broken link — there's no
// "create this page" affordance, keeping the feature to reading/writing
// pages that already exist.
func renderWikiLink(title string, titleIndex map[string]int64, projectID int64) string {
	if id, ok := titleIndex[strings.ToLower(strings.TrimSpace(title))]; ok {
		return fmt.Sprintf(`<a href="/projects/%s/wiki/%s" class="wiki-link">%s</a>`,
			strconv.FormatInt(projectID, 10), strconv.FormatInt(id, 10), title)
	}
	return title
}
