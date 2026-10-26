package seo

import "strings"

// RobotsIntent is what a page lets indexers do: index it, and follow its links.
type RobotsIntent struct{ Index, Follow bool }

func (r RobotsIntent) String() string {
	index, follow := "index", "follow"
	if !r.Index {
		index = "noindex"
	}
	if !r.Follow {
		follow = "nofollow"
	}
	return index + ", " + follow
}

// Robots collects the directives a rendered page gives indexers. Generic directives and those
// addressed to Googlebot or Bingbot (IndexNow's main reader) all bind an announced page, and the
// most restrictive one wins, as crawlers combine them. Preview and snippet controls such as
// max-image-preview or unavailable_after do not decide indexing and are ignored.
type Robots struct{ noindex, nofollow bool }

// Agents whose scoped directives bind the page; "robots" is the generic meta name.
var robotsAgents = map[string]bool{"robots": true, "googlebot": true, "bingbot": true}

// Directives whose own value follows a colon, so "name: value" is not a user-agent scope.
var robotsValued = map[string]bool{"max-snippet": true, "max-image-preview": true, "max-video-preview": true, "unavailable_after": true}

// Meta adds a <meta name content> tag. Names other than robots, googlebot and bingbot are ignored.
func (r *Robots) Meta(name, content string) {
	if !robotsAgents[strings.ToLower(strings.TrimSpace(name))] {
		return
	}
	for _, directive := range strings.Split(content, ",") {
		r.apply(directive)
	}
}

// Header adds one X-Robots-Tag value. "otherbot: noindex, googlebot: nofollow" switches the scope
// at each user-agent prefix; directives before any prefix apply to every crawler.
func (r *Robots) Header(value string) {
	binding := true
	for _, part := range strings.Split(value, ",") {
		directive := part
		if name, rest, ok := strings.Cut(part, ":"); ok {
			if agent := strings.ToLower(strings.TrimSpace(name)); robotsAgentName(agent) && !robotsValued[agent] {
				binding, directive = robotsAgents[agent], rest
			}
		}
		if binding {
			r.apply(directive)
		}
	}
}

// A user-agent token has no spaces, unlike the date fragments of unavailable_after ("15:00:00").
func robotsAgentName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func (r *Robots) apply(directive string) {
	switch strings.ToLower(strings.TrimSpace(directive)) {
	case "noindex":
		r.noindex = true
	case "nofollow":
		r.nofollow = true
	case "none":
		r.noindex, r.nofollow = true, true
	}
}

// Intent is the combined result; a page without directives may be indexed and followed.
func (r Robots) Intent() RobotsIntent { return RobotsIntent{Index: !r.noindex, Follow: !r.nofollow} }

// DecisionRobots reads a page decision's robots value, for example "noindex, follow".
func DecisionRobots(value string) RobotsIntent {
	var r Robots
	r.Meta("robots", value)
	return r.Intent()
}
