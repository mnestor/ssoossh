package confdocs

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ManPage renders the OPTIONS body of ssoosshd.yaml(5): one .SS per section,
// one .TP per key, with the default read from defaults.yaml.
//
// Only the body is generated. The surrounding NAME, DESCRIPTION, LOCATION,
// NOTES, ENVIRONMENT, and SEE ALSO sections are prose that no struct can
// produce, and they stay hand-written between the generated-region markers.
func ManPage(sections []*Section, defaults *Defaults) string {
	var b strings.Builder
	for _, s := range sections {
		refs := CrossRefsIn(sections, s.Key)
		fmt.Fprintf(&b, ".SS %s\n", manTitle(s))
		for _, line := range troffProse(sectionBody(s), refs, s.Key) {
			b.WriteString(line + "\n")
		}
		for _, f := range s.Fields {
			writeManField(&b, f, defaults, refs, s.Key, s.Key)
		}
	}
	return b.String()
}

// manTitle renders a section heading: the key plus a short gloss taken from
// the first sentence of the struct's doc comment.
func manTitle(s *Section) string {
	if s.Key == "" {
		return s.Title
	}
	gloss := firstSentence(s.Doc)
	if gloss == "" {
		return s.Key
	}
	return fmt.Sprintf("%s (%s)", s.Key, gloss)
}

// writeManField emits one key, recursing into nested structs so that
// http.tls.min_version appears under http rather than as its own section.
func writeManField(b *strings.Builder, f *Field, defaults *Defaults, refs map[string]string, scope, parent string) {
	if f.IsStruct() {
		for _, c := range f.Children {
			writeManField(b, c, defaults, refs, scope, f.Path)
		}
		return
	}

	// The embedded rotation options have no path of their own; they land in
	// the namespace of the struct that embeds them.
	if f.Embedded {
		b.WriteString(".TP\n")
		fmt.Fprintf(b, ".B %s.*\n", troffEscape(parent))
		for _, line := range troffProse(f.Doc, refs, scope) {
			b.WriteString(line + "\n")
		}
		return
	}

	b.WriteString(".TP\n")
	fmt.Fprintf(b, ".BI %s: \" %s\"\n", troffEscape(f.Path), f.Type)
	for _, line := range troffProse(f.Doc, refs, scope) {
		b.WriteString(line + "\n")
	}
	if d := defaults.Describe(f); d != "" {
		fmt.Fprintf(b, "Default:\n.BR %s .\n", troffEscape(d))
	}
}

// sentenceEnd matches the end of the first sentence, avoiding the common
// abbreviations that would otherwise cut a gloss short.
var sentenceEnd = regexp.MustCompile(`\.(\s|$)`)

// firstSentence returns a short gloss from a doc comment's opening sentence,
// for use inside a section heading's parentheses. Returns empty when nothing
// short and readable can be had, in which case the heading is just the key.
func firstSentence(doc []string) string {
	text := strings.TrimSpace(strings.Join(doc, " "))
	if text == "" {
		return ""
	}
	if loc := sentenceEnd.FindStringIndex(text); loc != nil {
		text = text[:loc[0]]
	}

	// "configures the HTTP(S) server: bind address, TLS, ..." reads better in
	// a heading as the list that follows the colon.
	if i := strings.Index(text, ":"); i > 0 && i < len(text)-1 {
		text = strings.TrimSpace(text[i+1:])
	}

	text = strings.TrimSpace(stripLeadingVerb(text))
	if text == "" {
		return ""
	}

	// Too long for a heading: keep the first clause, or give up.
	text = clauseCut(text)
	if text == "" {
		return ""
	}

	// Cutting at a comma can land inside a parenthetical, leaving the gloss
	// with an unclosed bracket.
	if strings.Count(text, "(") != strings.Count(text, ")") {
		return ""
	}

	// Lowercase the opening word, but never an acronym: "OAuth" must not
	// become "oAuth".
	if len(text) > 1 && text[1] >= 'a' && text[1] <= 'z' {
		text = strings.ToLower(text[:1]) + text[1:]
	}
	return text
}

// glossVerbs are the verbs a doc comment opens with by convention
// ("Configures the ..."); a heading wants the noun phrase that follows. The
// order is the order they are tried in, and the first match wins.
var glossVerbs = []string{"Configures ", "Holds ", "Carries ", "Groups ", "Selects ", "Records ", "Optionally configures "}

// stripLeadingVerb removes the opening verb from a gloss, if it has one.
func stripLeadingVerb(text string) string {
	for _, verb := range glossVerbs {
		if strings.HasPrefix(text, verb) {
			return strings.TrimPrefix(text, verb)
		}
	}
	return text
}

// clauseCut shortens a gloss too long for a heading to its first clause.
// Returns empty when no clause boundary falls early enough to cut at, which
// is the caller's signal to drop the gloss and leave the key to stand alone.
func clauseCut(text string) string {
	if len(text) <= glossMax {
		return text
	}
	cut := -1
	for _, sep := range []string{", ", "; "} {
		i := strings.Index(text, sep)
		if i <= 0 || i > glossMax {
			continue
		}
		if cut < 0 || i < cut {
			cut = i
		}
	}
	if cut < 0 {
		return ""
	}
	return text[:cut]
}

// glossMax is the longest section gloss worth putting in parentheses.
const glossMax = 72

// sectionBody is the section's doc minus its opening sentence when that
// sentence is already the heading's gloss. Printing it twice, once in
// parentheses and again immediately below, says nothing the second time.
func sectionBody(s *Section) []string {
	if firstSentence(s.Doc) == "" || len(s.Doc) == 0 {
		return s.Doc
	}
	text := strings.Join(s.Doc, " ")
	loc := sentenceEnd.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	rest := strings.TrimSpace(text[loc[1]:])
	if rest == "" {
		return nil
	}
	return []string{rest}
}

// goIdent matches a multi-word CamelCase identifier standing alone in prose:
// TrustedProxies, ValidDuration, CookieMaxAge.
//
// Deliberately narrow. A single-word name (Level, Port) and an acronym (FIPS,
// PAM, TLS) are both ordinary prose far more often than they are a reference,
// and rewriting those turned "FIPS 140-3" into "fips 140-3".
var goIdent = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:[A-Z][a-zA-Z0-9]*)+\b`)

// goOnlySee matches a "See Foo." or "See Foo and Bar." sentence naming only
// Go identifiers.
var goOnlySee = regexp.MustCompile(`\s*See ([A-Z][A-Za-z0-9]*(?:(?:,| and) [A-Z][A-Za-z0-9]*)*)\.`)

// StripGoOnlyRefs removes cross-references that point at Go identifiers with
// no configuration key behind them.
//
// "See DBProviderSqlite and DBProviderPostgres." helps someone reading the
// struct and means nothing to someone reading a config file or a man page. A
// reference that does resolve to a key is left alone, and so is a pointer at
// a document, since neither is Go-only.
//
// Works a paragraph at a time rather than a line at a time: doc comments wrap
// at 75 columns, so the sentence being removed routinely straddles two lines.
func StripGoOnlyRefs(lines []string, refs map[string]string) []string {
	var out []string
	var para []string

	flush := func() {
		if len(para) == 0 {
			return
		}
		text := goOnlySee.ReplaceAllStringFunc(strings.Join(para, " "), func(m string) string {
			for _, name := range identifier.FindAllString(strings.TrimPrefix(strings.TrimSpace(m), "See "), -1) {
				if _, ok := refs[name]; ok {
					return m
				}
			}
			return ""
		})
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
		para = nil
	}

	for _, l := range lines {
		switch {
		case strings.TrimSpace(l) == "":
			flush()
			out = append(out, "")
		case strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t"):
			// An indented block is an example a reader can copy; leave its
			// layout alone.
			flush()
			out = append(out, strings.TrimRight(l, " "))
		default:
			para = append(para, strings.TrimSpace(l))
		}
	}
	flush()

	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// identifier matches a single exported Go name.
var identifier = regexp.MustCompile(`[A-Z][A-Za-z0-9]*`)

// troffProse converts doc-comment lines into troff body text: Go field names
// rewritten to the config keys a reader of this page would recognise, blank
// lines turned into paragraph breaks, and every troff metacharacter escaped.
func troffProse(doc []string, refs map[string]string, scope string) []string {
	var out []string
	pendingBreak := false

	for _, line := range StripGoOnlyRefs(doc, refs) {
		if strings.TrimSpace(line) == "" {
			pendingBreak = true
			continue
		}
		if pendingBreak && len(out) > 0 {
			out = append(out, ".IP")
			pendingBreak = false
		}
		// A doc-comment code block (leading tab) is an example to copy, so it
		// is set literally rather than reflowed into the surrounding prose.
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
			out = append(out, ".nf", troffEscape(strings.TrimLeft(line, "\t ")), ".fi")
			continue
		}
		out = append(out, troffEscape(rewriteRefs(line, refs, scope)))
	}
	return out
}

// rewriteRefs replaces Go field names with their dotted config paths. A name
// is only rewritten when it resolves to exactly one key, so an ambiguous
// reference is left as written rather than pointed somewhere wrong.
//
// A field's own name is rewritten too. The doc-comment opener ("MaxIdleConns
// is the ...") is already removed by docLines, so anything left is a genuine
// reference to the key.
func rewriteRefs(line string, refs map[string]string, scope string) string {
	return goIdent.ReplaceAllStringFunc(line, func(word string) string {
		path, ok := refs[word]
		if !ok {
			return word
		}
		// A qualified reference (CertRequestService.Approve) names Go code,
		// not a config key. A sentence-ending period does not count, so the
		// test is for a dot followed by another identifier.
		if qualified(word).MatchString(line) {
			return word
		}
		// Inside its own section, the short key reads better than the full
		// dotted path: "see trusted_proxies" under .SS http.
		if scope != "" && strings.HasPrefix(path, scope+".") {
			return strings.TrimPrefix(path, scope+".")
		}
		return path
	})
}

// qualifiedCache memoises the per-word "Word.Something" patterns, which are
// otherwise recompiled for every identifier on every line.
var qualifiedCache sync.Map

// qualified builds the pattern matching word used as a Go qualifier.
func qualified(word string) *regexp.Regexp {
	if cached, ok := qualifiedCache.Load(word); ok {
		if re, ok := cached.(*regexp.Regexp); ok {
			return re
		}
	}
	re := regexp.MustCompile(regexp.QuoteMeta(word) + `\.[A-Za-z_]`)
	qualifiedCache.Store(word, re)
	return re
}

// troffReplacer escapes the characters that would otherwise be read as troff
// markup. The backslash must be handled first or it would double-escape the
// replacements themselves.
var troffReplacer = strings.NewReplacer(
	`\`, `\e`,
	"—", `\(em`,
	"–", `\(en`,
	"“", `\(lq`,
	"”", `\(rq`,
	"‘", "`",
	"’", "'",
	"…", "...",
)

// troffEscape makes one line safe to emit. A leading dot or apostrophe would
// be parsed as a request, so it is escaped with the zero-width \& prefix.
func troffEscape(line string) string {
	line = troffReplacer.Replace(line)
	line = strings.ReplaceAll(line, "-", `\-`)
	if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'") {
		line = `\&` + line
	}
	return line
}
