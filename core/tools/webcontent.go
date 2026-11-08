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
	clone := stripChrome(root, goquery.NodeName(root) == "article")

	// A main element holding only an intro, with the article beside it: the
	// page without its navigation keeps far more text, so use that instead.
	if goquery.NodeName(root) != "body" {
		page := stripChrome(body, false)
		if textLen(clone) < textLen(page)/4 {
			clone = page
		}
	}

	// A page that keeps its text inside nav or aside elements would come out
	// almost empty: give it back whole rather than return nothing.
	if kept := textLen(clone); kept < 200 && textLen(body) > kept+200 {
		return body.Clone()
	}
	return clone
}

// stripChrome returns a copy of root without navigation and scripts and, when
// root is not an article, without its layout header, footer and aside.
func stripChrome(root *goquery.Selection, isArticle bool) *goquery.Selection {
	clone := root.Clone()
	clone.Find(chromeSelector).Remove()
	if !isArticle {
		clone.Find("header, footer, aside").Each(func(_ int, s *goquery.Selection) {
			if s.ParentsFiltered("article, section").Length() == 0 {
				s.Remove()
			}
		})
	}
	return clone
}

func textLen(s *goquery.Selection) int { return len(strings.TrimSpace(s.Text())) }
