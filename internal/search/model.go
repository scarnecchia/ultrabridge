package search

// NoteDocument is the content record to index for one page of a note.
type NoteDocument struct {
	Path      string
	Page      int
	TitleText string
	BodyText  string
	Keywords  string
	Source    string // "myScript" or "api"
	Model     string // model used if source="api", empty otherwise
}

// SearchQuery is the input to a search operation.
type SearchQuery struct {
	Text  string
	Limit int // 0 = use default (25)
}

// SearchResult is one ranked result.
type SearchResult struct {
	Path    string
	Page    int
	Snippet string
	Score   float64 // bm25 score; used for ordering only, not displayed
}
