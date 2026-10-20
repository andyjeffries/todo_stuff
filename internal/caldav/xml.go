package caldav

import (
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// XML namespaces a CalDAV client asks about.
const (
	NSDAV    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
	NSCS     = "http://calendarserver.org/ns/" // getctag
	NSApple  = "http://apple.com/ns/ical/"     // calendar-color, calendar-order
)

var prefixes = map[string]string{NSDAV: "d", NSCalDAV: "c", NSCS: "cs", NSApple: "ic"}

// Node is a minimal XML DOM: enough to read PROPFIND, REPORT and PROPPATCH
// bodies without a struct per request shape.
type Node struct {
	Name     xml.Name
	Attr     []xml.Attr
	Children []*Node
	Text     string
}

// ParseXML reads a request body. An empty body is not an error: it returns
// nil, which PROPFIND treats as allprop.
func ParseXML(r io.Reader) (*Node, error) {
	dec := xml.NewDecoder(r)
	var stack []*Node
	var root *Node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return root, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name, Attr: t.Attr}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
}

// Is reports whether n is the element ns:local.
func (n *Node) Is(ns, local string) bool {
	return n != nil && n.Name.Space == ns && n.Name.Local == local
}

// Child returns the first direct child named ns:local, or nil.
func (n *Node) Child(ns, local string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Is(ns, local) {
			return c
		}
	}
	return nil
}

// AttrValue returns the value of an unqualified attribute.
func (n *Node) AttrValue(name string) string {
	for _, a := range n.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// Find returns every descendant named ns:local, depth first.
func (n *Node) Find(ns, local string) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(m *Node) {
		for _, c := range m.Children {
			if c.Is(ns, local) {
				out = append(out, c)
			}
			walk(c)
		}
	}
	if n != nil {
		walk(n)
	}
	return out
}

// RequestedProps returns the property names inside a <prop> element. A nil
// result with allprop=true means "everything you have".
func RequestedProps(body *Node) (names []xml.Name, allprop bool) {
	if body == nil || body.Child(NSDAV, "allprop") != nil {
		return nil, true
	}
	prop := body.Child(NSDAV, "prop")
	if prop == nil {
		return nil, true
	}
	for _, c := range prop.Children {
		names = append(names, c.Name)
	}
	return names, false
}

// Prop is one property value in a response. Inner is already-serialised XML
// (or escaped text) using the d:/c:/cs:/ic: prefixes Multistatus declares.
type Prop struct {
	Name  xml.Name
	Inner string
}

// Response is one <d:response>: the properties found, and the requested
// names that weren't (reported as 404 in their own propstat). Status, when
// set, replaces the propstats with a bare status (e.g. a missing href in a
// multiget).
type Response struct {
	Href     string
	Found    []Prop
	NotFound []xml.Name
	Status   int
}

// Multistatus renders a 207 body.
func Multistatus(responses []Response) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/">`)
	for _, r := range responses {
		b.WriteString("<d:response><d:href>")
		b.WriteString(Escape(r.Href))
		b.WriteString("</d:href>")
		if r.Status != 0 {
			b.WriteString("<d:status>" + statusLine(r.Status) + "</d:status>")
		} else {
			if len(r.Found) > 0 {
				b.WriteString("<d:propstat><d:prop>")
				for _, p := range r.Found {
					writeElement(&b, p.Name, p.Inner)
				}
				b.WriteString("</d:prop><d:status>" + statusLine(http.StatusOK) + "</d:status></d:propstat>")
			}
			if len(r.NotFound) > 0 {
				b.WriteString("<d:propstat><d:prop>")
				for _, n := range r.NotFound {
					writeElement(&b, n, "")
				}
				b.WriteString("</d:prop><d:status>" + statusLine(http.StatusNotFound) + "</d:status></d:propstat>")
			}
		}
		b.WriteString("</d:response>")
	}
	b.WriteString("</d:multistatus>")
	return []byte(b.String())
}

// PropstatForbidden renders a PROPPATCH response refusing every property.
func PropstatForbidden(href string, names []xml.Name) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/" xmlns:ic="http://apple.com/ns/ical/">`)
	b.WriteString("<d:response><d:href>" + Escape(href) + "</d:href><d:propstat><d:prop>")
	for _, n := range names {
		writeElement(&b, n, "")
	}
	b.WriteString("</d:prop><d:status>" + statusLine(http.StatusForbidden) + "</d:status></d:propstat></d:response></d:multistatus>")
	return []byte(b.String())
}

// Error renders a DAV:error body naming a failed precondition, e.g.
// Error(NSCalDAV, "supported-calendar-component").
func Error(ns, local string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<d:error xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`)
	writeElement(&b, xml.Name{Space: ns, Local: local}, "")
	b.WriteString("</d:error>")
	return []byte(b.String())
}

func writeElement(b *strings.Builder, n xml.Name, inner string) {
	tag, decl := qualify(n)
	b.WriteString("<" + tag + decl)
	if inner == "" {
		b.WriteString("/>")
		return
	}
	b.WriteString(">" + inner + "</" + tag + ">")
}

// qualify returns the prefixed tag for a name, declaring its namespace
// inline when it isn't one of ours (echoing an unknown requested property).
func qualify(n xml.Name) (tag, decl string) {
	if p, ok := prefixes[n.Space]; ok {
		return p + ":" + n.Local, ""
	}
	if n.Space == "" {
		return n.Local, ""
	}
	return "x:" + n.Local, ` xmlns:x="` + Escape(n.Space) + `"`
}

func statusLine(code int) string {
	return "HTTP/1.1 " + strconv.Itoa(code) + " " + http.StatusText(code)
}

// Escape is XML text escaping.
func Escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Href renders <d:href>…</d:href> for property values.
func Href(h string) string { return "<d:href>" + Escape(h) + "</d:href>" }
