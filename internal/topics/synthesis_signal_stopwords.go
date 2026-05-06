package topics

var baseSignalStopwords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "all": {}, "also": {}, "an": {}, "and": {}, "any": {},
	"are": {}, "around": {}, "because": {}, "been": {}, "between": {}, "bookmark": {}, "bookmarks": {},
	"build": {}, "building": {}, "built": {}, "can": {}, "could": {}, "data": {}, "does": {}, "enough": {},
	"co": {}, "com": {}, "even": {}, "for": {}, "from": {}, "gets": {}, "getting": {}, "github": {}, "have": {}, "here": {},
	"how": {}, "http": {}, "https": {}, "into": {}, "just": {}, "like": {}, "links": {}, "linked": {}, "look": {}, "looking": {},
	"looks": {}, "made": {}, "make": {}, "makes": {}, "many": {}, "more": {}, "most": {}, "much": {},
	"note": {}, "notes": {}, "onto": {}, "other": {}, "over": {}, "pic": {}, "post": {}, "posts": {}, "repo": {},
	"repos": {}, "saved": {}, "save": {}, "saving": {}, "show": {}, "showing": {}, "shows": {}, "source": {},
	"sources": {}, "status": {}, "such": {}, "t": {}, "than": {}, "that": {}, "their": {}, "them": {}, "then": {}, "there": {},
	"these": {}, "they": {}, "this": {}, "those": {}, "through": {}, "tweet": {}, "tweets": {}, "using": {},
	"used": {}, "user": {}, "users": {}, "video": {}, "videos": {}, "what": {}, "when": {}, "where": {},
	"which": {}, "while": {}, "who": {}, "why": {}, "will": {}, "with": {}, "without": {}, "work": {},
	"worked": {}, "working": {}, "works": {}, "www": {}, "x": {}, "your": {},
}

var genericSingleSignalStopwords = map[string]struct{}{
	"article": {}, "articles": {}, "headline": {}, "headlines": {}, "latest": {}, "local": {},
	"national": {}, "news": {}, "opinion": {}, "read": {}, "reading": {}, "story": {},
	"stories": {}, "subscribe": {}, "subscription": {}, "today": {},
}
