package tools

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// chromeSelector names elements that are never page content: scripts and the
// site's navigation (menus, sidebars, tables of contents, search boxes).
const chromeSelector = "script, style, noscript, template, nav, [role=navigation], [role=search]"

// contentRoot returns a detached copy of the part of the page that holds its
// content, with the navigation removed. On a documentation site the menus and
// tables of contents come before the article and can fill a whole fetch
// budget on their own; this is what keeps them out. raw keeps the whole body.
//
// The copy is safe to mutate: doc is never changed.
func contentRoot(doc *goquery.Document, raw bool) *goquery.Selection {
	body := doc.Find("body").First()
	if body.Length() == 0 {
		body = doc.Selection
	}
	if raw {
		return body.Clone()
	}

	root := doc.Find("main, [role=main]").First()
	isMain := root.Length() > 0
	if isMain {
		if arts := root.Find("article"); arts.Length() == 1 {
			root = arts
		}
	} else if arts := doc.Find("article"); arts.Length() == 1 {
		root = arts
	} else {
		root = body
	}
	isArticle := goquery.NodeName(root) == "article"

	clone := root.Clone()
	clone.Find(chromeSelector).Remove()
	if !isArticle {
		clone.Find("header, footer, aside").Each(func(_ int, s *goquery.Selection) {
			if s.ParentsFiltered("article, section").Length() == 0 {
				s.Remove()
			}
		})
	}

	// A page that keeps its text inside nav or aside elements would come out
	// empty: give it back whole rather than return nothing.
	if len(strings.TrimSpace(clone.Text())) < 200 && len(strings.TrimSpace(body.Text())) >= 1000 {
		return body.Clone()
	}
	return clone
}
