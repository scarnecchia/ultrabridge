package search

// NoteDocument is the content record to index for one page of a note.
//
// JSON tags are snake_case to match the decoupled-architecture API v1
// convention — these are emitted verbatim by /api/notes/pages which MCP
// clients consume. Without the tags, Go's default PascalCase field names
// leaked into the API response and broke downstream decoders.
type NoteDocument struct {
	Path      string `json:"path"`
	Page      int    `json:"page"`
	TitleText string `json:"title_text"`
	BodyText  string `json:"body_text"`
	Keywords  string `json:"keywords"`
	Source    string `json:"source"` // "myScript" or "api"
	Model     string `json:"model"`  // model used if source="api", empty otherwise
}

// SearchQuery is the input to a search operation.
type SearchQuery struct {
	Text   string
	Folder string // filter by folder path segment (empty = all folders)
	Limit  int    // 0 = use default (25)
}

// SearchResult is one ranked result.
type SearchResult struct {
	Path    string
	Page    int
	Snippet string
	Score   float64 // bm25 score; used for ordering only, not displayed
}
