package builtin

// ReadInput is the typed input for the read tool.
type ReadInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// WriteInput is the typed input for the write tool.
type WriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// EditInput is the typed input for the edit tool.
type EditInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// MutateInput is the typed input for the mutate tool.
type MutateInput struct {
	Operations []MutateOperation `json:"operations"`
	DryRun     bool              `json:"dry_run,omitempty"`
}

// MutateOperation is one ordered file mutation in a mutate call.
type MutateOperation struct {
	Type       string `json:"type"`
	Path       string `json:"path,omitempty"`
	Content    string `json:"content,omitempty"`
	OldString  string `json:"old_string,omitempty"`
	NewString  string `json:"new_string,omitempty"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
	Line       int    `json:"line,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
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
)

// NormalizeRead applies defaults and caps to read input.
func NormalizeRead(in *ReadInput) {
	if in.Offset <= 0 {
		in.Offset = 1
	}
	if in.Limit <= 0 {
		in.Limit = defaultReadLimit
	}
	in.Limit = min(in.Limit, maxReadLimit)
}

// NormalizeGlob applies defaults and caps to glob input.
func NormalizeGlob(in *GlobInput) {
	if in.Offset <= 0 {
		in.Offset = 0
	}
	if in.Limit <= 0 {
		in.Limit = defaultGlobLimit
	}
	in.Limit = min(in.Limit, maxGlobLimit)
}

// NormalizeGrep applies defaults and caps to grep input.
func NormalizeGrep(in *GrepInput) {
	if in.HeadLimit <= 0 {
		in.HeadLimit = defaultGrepHeadLimit
	}
	in.HeadLimit = min(in.HeadLimit, maxGrepHeadLimit)
	if in.Offset <= 0 {
		in.Offset = 0
	}
}

// NormalizeLS applies defaults and caps to ls input.
func NormalizeLS(in *LSInput) {
	if in.Offset <= 0 {
		in.Offset = 0
	}
	if in.Limit <= 0 {
		in.Limit = defaultLSLimit
	}
	in.Limit = min(in.Limit, maxLSLimit)
}

// NormalizeBash applies defaults and caps to bash input.
func NormalizeBash(in *BashInput) {
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = defaultBashTimeoutSeconds
	}
	in.TimeoutSeconds = min(in.TimeoutSeconds, maxBashTimeoutSeconds)
	if in.MaxOutputChars <= 0 {
		in.MaxOutputChars = defaultBashMaxOutputChars
	}
	in.MaxOutputChars = min(in.MaxOutputChars, maxBashMaxOutputChars)
}

// NormalizeDisplayFile applies defaults and caps to display_file input.
func NormalizeDisplayFile(in *DisplayFileInput) {
	if in.Offset <= 0 {
		in.Offset = 1
	}
	if in.Limit <= 0 {
		in.Limit = defaultDisplayFileLimit
	}
	in.Limit = min(in.Limit, maxDisplayFileLimit)
}

// NormalizeFetchURL applies defaults and caps to fetch_url input.
func NormalizeFetchURL(in *FetchURLInput) {
	if in.MaxSize <= 0 {
		in.MaxSize = 500000
	}
	if in.MaxSize > 1000000 {
		in.MaxSize = 1000000
	}
}
