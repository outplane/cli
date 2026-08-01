package core

import (
	"fmt"
	"regexp"
	"strings"
)

// Query construction for runtime logs.
//
// Every function here is pure: a query is a string built from arguments, with
// no client, no clock and no configuration. That is deliberate, because a
// wrong selector is silent — it returns an empty result that reads exactly
// like "your application printed nothing" — so the only way to be sure of one
// is to be able to check it without a network.
//
// The query language is the log gateway's own. The shape below mirrors what
// the console sends, and it has to keep mirroring it: two clients disagreeing
// about which stream belongs to an application would show two different sets
// of logs for the same app.

// LogLevel narrows a query to lines that look like a given severity.
type LogLevel string

const (
	LevelError   LogLevel = "error"
	LevelWarning LogLevel = "warning"
	LevelInfo    LogLevel = "info"
	LevelDebug   LogLevel = "debug"
	LevelTrace   LogLevel = "trace"
)

// levelKeywords is what each level actually matches.
//
// Severity is not a label on the stream; it is inferred from the words in the
// line. That is why "info" maps to nothing: it is the absence of the other
// keywords, which cannot be expressed as a positive match. Asking for info, or
// for everything, therefore applies no filter at all rather than a filter that
// would quietly drop lines.
var levelKeywords = map[LogLevel][]string{
	LevelError:   {"error", "fatal"},
	LevelWarning: {"warn"},
	LevelInfo:    nil,
	LevelDebug:   {"debug"},
	LevelTrace:   {"trace"},
}

// AllLevels is every level a caller may ask for, in severity order.
var AllLevels = []LogLevel{LevelError, LevelWarning, LevelInfo, LevelDebug, LevelTrace}

// ParseLevel accepts a level name, or reports the ones that exist.
func ParseLevel(s string) (LogLevel, error) {
	l := LogLevel(strings.ToLower(strings.TrimSpace(s)))
	if _, ok := levelKeywords[l]; ok {
		return l, nil
	}
	names := make([]string, len(AllLevels))
	for i, v := range AllLevels {
		names[i] = string(v)
	}
	return "", fmt.Errorf("no log level called %q, expected one of: %s", s, strings.Join(names, ", "))
}

// LogFilter narrows a runtime log query.
type LogFilter struct {
	// Apps limits the query to these applications. Empty means the whole team.
	Apps []string

	// Search is a case-insensitive substring the line must contain.
	Search string

	// Levels keeps only lines that look like one of these. Empty means all.
	Levels []LogLevel
}

// BuildRuntimeQuery assembles the selector and its line filters.
//
// Filtering happens at the gateway rather than here. Pulling everything back
// and discarding most of it locally would work for a quiet application and
// fall over on a busy one, which is exactly when somebody is reading logs.
func BuildRuntimeQuery(teamSlug string, f LogFilter) string {
	q := runtimeSelector(teamSlug, f.Apps)

	if s := strings.TrimSpace(f.Search); s != "" {
		q += fmt.Sprintf(" |~ `(?i)%s`", quoteMeta(s))
	}
	q += levelClause(f.Levels)

	return q
}

// runtimeSelector picks the streams an application writes to.
//
// The namespace is the team slug, which is why a token that cannot name its
// team cannot read logs at all. Application names are DNS labels, so they need
// no escaping inside the pattern.
func runtimeSelector(teamSlug string, apps []string) string {
	switch len(apps) {
	case 0:
		return fmt.Sprintf(`{namespace=%q,managed_by="planeout"}`, teamSlug)
	case 1:
		return fmt.Sprintf(`{namespace=%q,app_name=%q}`, teamSlug, apps[0])
	default:
		return fmt.Sprintf(`{namespace=%q,app_name=~"%s"}`, teamSlug, strings.Join(apps, "|"))
	}
}

// levelClause turns a set of levels into one keyword match.
//
// It returns nothing in two cases, and both are deliberate. Selecting every
// level is the same as selecting none, and selecting a set that includes info
// cannot be narrowed at all, because info is defined by the absence of the
// other keywords. Returning an empty clause shows more than asked for; a
// positive filter would show less, and losing lines from a log is worse than
// seeing extra ones.
func levelClause(levels []LogLevel) string {
	if len(levels) == 0 || len(levels) == len(AllLevels) {
		return ""
	}

	var keywords []string
	for _, l := range levels {
		if l == LevelInfo {
			return ""
		}
		keywords = append(keywords, levelKeywords[l]...)
	}
	if len(keywords) == 0 {
		return ""
	}
	return fmt.Sprintf(" |~ `(?i)(%s)`", strings.Join(keywords, "|"))
}

// quoteMeta escapes a user's text so it matches literally.
//
// Without this, searching for a line containing "(" is a syntax error the
// gateway reports rather than a search, and searching for "." quietly matches
// every line.
func quoteMeta(s string) string { return regexp.QuoteMeta(s) }
