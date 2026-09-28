package handler

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/ibrahimijai/sh.sh/internal/service"
)

type previewData struct {
	Found       bool
	ShortURL    string
	TargetURL   string
	TargetHost  string
	Title       string
	CreatedAt   string
	ContinueURL string
}

// renderPreview serves /{slug}+ : where a short link goes, without redirecting. "Continue" goes
// through the normal redirect, so the visit is still counted.
func (h *RedirectHandler) renderPreview(c *gin.Context, slug string, link service.RedirectTarget, found bool) {
	data := previewData{Found: found, ShortURL: shortURL(h.baseURL, slug)}
	status := http.StatusNotFound
	if found {
		status = http.StatusOK
		data.TargetURL = link.TargetURL
		if parsed, err := url.Parse(link.TargetURL); err == nil {
			data.TargetHost = parsed.Hostname()
		}
		data.Title = link.Title
		data.CreatedAt = link.CreatedAt.UTC().Format("2 January 2006")
		data.ContinueURL = "/" + link.Slug
		if query := c.Request.URL.RawQuery; query != "" {
			data.ContinueURL += "?" + query
		}
	}

	var page bytes.Buffer
	if err := previewTemplate.Execute(&page, data); err != nil {
		log.Printf("render preview for %q: %v", slug, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not render preview"})
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	c.Data(status, "text/html; charset=utf-8", page.Bytes())
}

var previewTemplate = template.Must(template.New("preview").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{if .Found}}Link preview: {{.TargetHost}}{{else}}Link not found{{end}}</title>
<style>
  :root {
    --bg: #f6f7f9; --card: #ffffff; --text: #16181d; --muted: #5f6673;
    --border: #e3e6eb; --accent: #1f6feb; --accent-text: #ffffff; --code-bg: #f0f2f5;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #0f1115; --card: #181b21; --text: #e8eaee; --muted: #9aa3b2;
      --border: #2a2f38; --accent: #4c8dff; --accent-text: #0b0d10; --code-bg: #20242c;
    }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 24px 16px;
    background: var(--bg); color: var(--text);
    font: 16px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  }
  main {
    width: 100%; max-width: 520px; background: var(--card);
    border: 1px solid var(--border); border-radius: 14px; padding: 28px;
  }
  .label { margin: 0 0 4px; font-size: 13px; color: var(--muted); letter-spacing: .02em; }
  h1 { margin: 0 0 16px; font-size: 26px; line-height: 1.25; overflow-wrap: anywhere; }
  .title { margin: 0 0 12px; font-weight: 600; }
  .url {
    margin: 0 0 16px; padding: 10px 12px; border-radius: 8px; background: var(--code-bg);
    font: 14px/1.45 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; overflow-wrap: anywhere;
  }
  .meta { margin: 0 0 24px; font-size: 14px; color: var(--muted); }
  .button {
    display: block; text-align: center; padding: 12px 16px; border-radius: 10px;
    background: var(--accent); color: var(--accent-text); font-weight: 600; text-decoration: none;
  }
  .note { margin: 12px 0 0; font-size: 13px; color: var(--muted); text-align: center; }
</style>
</head>
<body>
<main>
{{if .Found}}
  <p class="label">{{.ShortURL}} goes to</p>
  <h1>{{.TargetHost}}</h1>
  {{if .Title}}<p class="title">{{.Title}}</p>{{end}}
  <p class="url">{{.TargetURL}}</p>
  <p class="meta">Created {{.CreatedAt}}</p>
  <a class="button" href="{{.ContinueURL}}">Continue to {{.TargetHost}}</a>
  <p class="note">Only continue if you trust this site.</p>
{{else}}
  <p class="label">{{.ShortURL}}</p>
  <h1>This link doesn't exist</h1>
  <p class="meta">It may have been removed, disabled, or expired.</p>
{{end}}
</main>
</body>
</html>
`))
