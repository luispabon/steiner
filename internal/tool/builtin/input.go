package builtin

// ReadInput is the typed input for the read tool.
type ReadInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// MutateInput is the typed input for the mutate tool.
type MutateInput struct {
	Operations []MutateOperation `json:"operations"`
}

// MutateOperation is one ordered file mutation in a mutate call.
type MutateOperation struct {
	Type          string   `json:"type"`
	Path          string   `json:"path,omitempty"`
	Content       string   `json:"content,omitempty"`
	OldString     string   `json:"old_string,omitempty"`
	NewString     string   `json:"new_string,omitempty"`
	AssertPresent []string `json:"assert_present,omitempty"`
	AssertAbsent  []string `json:"assert_absent,omitempty"`
	ReplaceAll    bool     `json:"replace_all,omitempty"`
	FileHash      string   `json:"file_hash,omitempty"`
	From          string   `json:"from,omitempty"`
	To            string   `json:"to,omitempty"`
}

// GlobInput is the typed input for the glob tool.
type GlobInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Offset  int    `json:"offset,omitempty"`
}

// GrepInput is the typed input for the grep tool.
type GrepInput struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	Type            string `json:"type,omitempty"`
	OutputMode      string `json:"output_mode,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	LineNumbers     *bool  `json:"line_numbers,omitempty"`
	AfterContext    int    `json:"after_context,omitempty"`
	BeforeContext   int    `json:"before_context,omitempty"`
	Context         int    `json:"context,omitempty"`
	Multiline       bool   `json:"multiline,omitempty"`
	HeadLimit       int    `json:"head_limit,omitempty"`
	Offset          int    `json:"offset,omitempty"`
}

// LSInput is the typed input for the ls tool.
type LSInput struct {
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// DisplayFileInput is the typed input for the display_file tool.
type DisplayFileInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// BashInput is the typed input for the bash tool.
type BashInput struct {
	Command        string `json:"command"`
	CWD            string `json:"cwd,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	MaxOutputChars int    `json:"max_output_chars,omitempty"`
}

// FetchURLInput is the typed input for the fetch_url tool.
type FetchURLInput struct {
	URL     string `json:"url"`
	MaxSize int    `json:"max_size,omitempty"`
}

// WebSearchInput is the typed input for the web_search tool.
type WebSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

const (
	defaultReadLimit          = 200
	maxReadLimit              = 1000
	defaultGlobLimit          = 200
	maxGlobLimit              = 1000
	defaultLSLimit            = 200
	maxLSLimit                = 1000
	defaultGrepHeadLimit      = 100
	maxGrepHeadLimit          = 500
	defaultDisplayFileLimit   = 120
	maxDisplayFileLimit       = 1000
	defaultBashTimeoutSeconds = 30
	maxBashTimeoutSeconds     = 120
	defaultBashMaxOutputChars = 30000
	maxBashMaxOutputChars     = 100000
	defaultFetchURLMaxSize    = 10 << 20
	maxFetchURLMaxSize        = 10 << 20
	defaultWebSearchLimit     = 10
	maxWebSearchLimit         = 30
)

// normalizeRead applies defaults and caps to read input.
func normalizeRead(in *ReadInput) {
	if in.Offset <= 0 {
		in.Offset = 1
	}
	if in.Limit <= 0 {
		in.Limit = defaultReadLimit
	}
	in.Limit = min(in.Limit, maxReadLimit)
}

// normalizeGlob applies defaults and caps to glob input.
func normalizeGlob(in *GlobInput) {
	if in.Offset <= 0 {
		in.Offset = 0
	}
	if in.Limit <= 0 {
		in.Limit = defaultGlobLimit
	}
	in.Limit = min(in.Limit, maxGlobLimit)
}

// normalizeGrep applies defaults and caps to grep input.
func normalizeGrep(in *GrepInput) {
	if in.HeadLimit <= 0 {
		in.HeadLimit = defaultGrepHeadLimit
	}
	in.HeadLimit = min(in.HeadLimit, maxGrepHeadLimit)
	if in.Offset <= 0 {
		in.Offset = 0
	}
}

// normalizeLS applies defaults and caps to ls input.
func normalizeLS(in *LSInput) {
	if in.Offset <= 0 {
		in.Offset = 0
	}
	if in.Limit <= 0 {
		in.Limit = defaultLSLimit
	}
	in.Limit = min(in.Limit, maxLSLimit)
}

// normalizeBash applies defaults and caps to bash input.
func normalizeBash(in *BashInput) {
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = defaultBashTimeoutSeconds
	}
	in.TimeoutSeconds = min(in.TimeoutSeconds, maxBashTimeoutSeconds)
	if in.MaxOutputChars <= 0 {
		in.MaxOutputChars = defaultBashMaxOutputChars
	}
	in.MaxOutputChars = min(in.MaxOutputChars, maxBashMaxOutputChars)
}

// normalizeDisplayFile applies defaults and caps to display_file input.
func normalizeDisplayFile(in *DisplayFileInput) {
	if in.Offset <= 0 {
		in.Offset = 1
	}
	if in.Limit <= 0 {
		in.Limit = defaultDisplayFileLimit
	}
	in.Limit = min(in.Limit, maxDisplayFileLimit)
}

// normalizeFetchURL applies defaults and caps to fetch_url input.
func normalizeFetchURL(in *FetchURLInput) {
	if in.MaxSize <= 0 {
		in.MaxSize = defaultFetchURLMaxSize
	}
	if in.MaxSize > maxFetchURLMaxSize {
		in.MaxSize = maxFetchURLMaxSize
	}
}

// normalizeWebSearch applies defaults and caps to web_search input.
func normalizeWebSearch(in *WebSearchInput) {
	if in.Limit <= 0 {
		in.Limit = defaultWebSearchLimit
	}
	if in.Limit > maxWebSearchLimit {
		in.Limit = maxWebSearchLimit
	}
}
