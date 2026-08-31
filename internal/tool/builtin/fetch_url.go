package builtin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/dive/toolkit"
	"github.com/deepnoodle-ai/wonton/fetch"

	"github.com/luispabon/steiner/internal/tool"
)

// inlineThreshold is the maximum number of content runes returned inline
// before content is saved to disk and a preview is returned instead.
const inlineThreshold = 10000

// savedContentPreviewRunes is the number of leading content runes returned
// inline alongside file_path for disk-saved results — enough to judge
// relevance before calling read, not enough to duplicate the saved content.
const savedContentPreviewRunes = 500

// mainContentFallbackMinRunes is the minimum rune count expected from
// main-content extraction. Markdown shorter than this is treated as a
// failed extraction and triggers a fallback fetch of the full document.
const mainContentFallbackMinRunes = 200

// mainContentFallbackMessage is the advisory set on the result when
// main-content extraction found nothing usable and the full document was
// returned instead.
const mainContentFallbackMessage = "Main-content extraction found nothing usable; returned the full document instead."

// fetchUserAgent identifies steiner on outbound fetch_url requests. This is
// an honest, identifying User-Agent, not an attempt to evade bot detection;
// Go's default User-Agent gets blocked by Cloudflare-fronted sites.
const fetchUserAgent = "steiner (+https://github.com/luispabon/steiner)"

// FetchURLResult is the result from a fetch_url tool call.
type FetchURLResult struct {
	URL           string      `json:"url"`
	Title         string      `json:"title,omitempty"`
	Description   string      `json:"description,omitempty"`
	Content       string      `json:"content"`
	ContentLength int         `json:"content_length"`
	StatusCode    int         `json:"status_code,omitempty"`
	Image         *ImageBlock `json:"image,omitempty"`
	FilePath      string      `json:"file_path,omitempty"`
	NextOffset    int         `json:"next_offset,omitempty"`
	Message       string      `json:"message,omitempty"`
	TotalLines    int         `json:"total_lines,omitempty"`
}

// NewFetchURLTool creates a ToolDef for the fetch_url tool.
// nolint:gocyclo // handler closure complexity is unavoidable with multi-branch content-type routing and image extension fallback
func NewFetchURLTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "fetch_url",
		ParallelSafe:    true,
		Description:     "Fetch a URL and return its content. Supports HTML (main content extracted and converted to markdown, falling back to the full document if extraction finds nothing), text formats (JSON, YAML, plain text, CSV, etc.), and images (png, jpeg, gif, webp). Images are always saved to .steiner/tmp/fetched; use the read tool with the returned file path to inspect them. Large responses are saved to disk in full — use the read tool to paginate; the only ceiling is the max_size download/save limit.",
		ParameterSchema: FetchURLSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[FetchURLInput](input)
			if err != nil {
				return nil, fmt.Errorf("fetch_url: %w", err)
			}

			if in.URL == "" {
				return nil, fmt.Errorf("fetch_url: invalid url: %w", fmt.Errorf("empty url"))
			}

			parsedURL, err := url.Parse(in.URL)
			if err != nil {
				return nil, fmt.Errorf("fetch_url: invalid url: %w", err)
			}

			if parsedURL.Scheme == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
				return nil, fmt.Errorf("fetch_url: invalid url: %w", fmt.Errorf("missing or invalid scheme"))
			}

			normalizeFetchURL(&in)

			httpClient := toolkit.SafeHTTPClient(15 * time.Second)
			if env.httpClient != nil {
				httpClient = env.httpClient()
			}

			// Peek at Content-Type via HEAD to decide how to handle the response.
			contentType := ""
			headReq, headErr := http.NewRequestWithContext(ctx, http.MethodHead, in.URL, nil)
			if headErr == nil {
				headReq.Header.Set("User-Agent", fetchUserAgent)
				headResp, doErr := httpClient.Do(headReq)
				if doErr == nil {
					contentType = headResp.Header.Get("Content-Type")
					_ = headResp.Body.Close()
				}
				// On HEAD failure, fall through to wonton/fetch.
			}

			// Decide how to handle based on Content-Type. The same decision
			// is reused by handleFetchError's unexpected-content-type
			// recovery path, so the two cannot drift apart.
			if result, handled, resErr := routeByContentType(ctx, httpClient, in, env.WorkDir, contentType); handled {
				return result, resErr
			}

			fetcher := fetch.NewHTTPFetcher(fetch.HTTPFetcherOptions{
				Client:      httpClient,
				Timeout:     15 * time.Second,
				MaxBodySize: int64(in.MaxSize),
			})

			// raw_html is requested on every HTML fetch, not just ones that
			// end up needing the fallback below, so the fallback can
			// reprocess it locally instead of re-fetching. That retains a
			// copy of the raw body (up to MaxBodySize) on the response for
			// the duration of this handler on every HTML fetch, not only
			// the rare fallback case.
			req := &fetch.Request{
				URL:             in.URL,
				Formats:         []string{"markdown", "raw_html"},
				ExcludeTags:     toolkit.DefaultFetchExcludeTags,
				OnlyMainContent: true,
				Headers:         map[string]string{"User-Agent": fetchUserAgent},
			}

			resp, err := fetcher.Fetch(ctx, req)
			if err != nil {
				return handleFetchError(ctx, httpClient, in, env.WorkDir, err)
			}

			fallbackMessage := ""
			if resp.StatusCode == 200 && resp.RawHTML != "" && utf8.RuneCountInString(resp.Markdown) < mainContentFallbackMinRunes {
				rawHTML := resp.RawHTML
				fullReq := &fetch.Request{
					URL:         in.URL,
					Formats:     []string{"markdown"},
					ExcludeTags: toolkit.DefaultFetchExcludeTags,
					Headers:     map[string]string{"User-Agent": fetchUserAgent},
				}
				// ProcessRequest's only failure mode is HTML parse failure,
				// which isn't expected from a body wonton itself already
				// parsed once inside Fetch. The error is still returned, so
				// it is handled rather than discarded: on a surprise
				// failure, keep the original near-empty main-content result
				// rather than erroring the whole call.
				if fullResp, fullErr := fetch.ProcessRequest(fullReq, rawHTML); fullErr == nil {
					// ProcessRequest only processes HTML; it doesn't set
					// URL/StatusCode/Headers the way Fetch does, so those
					// must be carried over from the original response or
					// buildHTMLResult reads a zero-value StatusCode and
					// takes the non-200 error branch.
					fullResp.URL = resp.URL
					fullResp.StatusCode = resp.StatusCode
					fullResp.Headers = resp.Headers
					resp = fullResp
					fallbackMessage = mainContentFallbackMessage
				}
			}

			// Nothing downstream reads RawHTML; drop it before the
			// content/truncation copies buildHTMLResult makes so its peak
			// memory doesn't overlap with the raw body's.
			resp.RawHTML = ""

			return buildHTMLResult(in.URL, resp, env.WorkDir, in.MaxSize, fallbackMessage)
		},
	}
}

// routeByContentType applies fetch_url's content-type routing decision:
// image types are fetched and saved as images, text-like non-HTML types are
// fetched as raw text, and unsupported types return an error result. It
// reports handled=false when contentType is HTML, or is unclassifiable (empty
// or application/octet-stream) with no image-suggesting URL extension — the
// caller must then run wonton/fetch's HTML pipeline itself. handleFetchError's
// unexpected-content-type recovery path calls this same function so a content
// type discovered late behaves identically to one HEAD reported up front.
func routeByContentType(ctx context.Context, httpClient *http.Client, in FetchURLInput, workDir, contentType string) (result any, handled bool, err error) {
	switch {
	case isImageContentType(contentType):
		result, err := fetchAndSaveImage(ctx, httpClient, in, workDir, contentType)
		return result, true, err

	case contentType == "" || cleanContentType(contentType) == "application/octet-stream":
		// Extension fallback: treat as image if URL suggests an image.
		if hasImageExtension(in.URL) {
			result, err := fetchAndSaveImage(ctx, httpClient, in, workDir, contentType)
			return result, true, err
		}
		return nil, false, nil

	case isTextLikeContentType(contentType) && !isHTMLContentType(contentType) && contentType != "":
		result, err := fetchRawText(ctx, httpClient, in, workDir, contentType)
		return result, true, err

	case !isTextLikeContentType(contentType):
		return newFetchURLError(in.URL, fmt.Sprintf("unsupported content type: %s", cleanContentType(contentType))), true, nil

	default:
		// contentType indicates HTML; the caller runs wonton/fetch itself.
		return nil, false, nil
	}
}

// fetchAndSaveImage fetches the image at in.URL and saves it to workDir.
func fetchAndSaveImage(ctx context.Context, httpClient *http.Client, in FetchURLInput, workDir, contentType string) (any, error) {
	img, statusCode, imgErr := fetchImageBytes(ctx, httpClient, in.URL, contentType)
	if imgErr != nil {
		return newFetchURLError(in.URL, imgErr.Error()), nil
	}
	result, saveErr := saveFetchedImage(workDir, img)
	if saveErr != nil {
		return nil, fmt.Errorf("fetch_url: %w", saveErr)
	}
	result.URL = in.URL
	result.StatusCode = statusCode
	return result, nil
}

// buildHTMLResult converts a successful wonton/fetch response into a
// fetch_url result, applying max_size truncation and the inline/disk-saved
// inlineThreshold gate. inURL is the originally requested URL, used for
// error results (resp.URL reflects the final URL after redirects and is
// used for success results). fallbackMessage, if non-empty, is composed
// ahead of any nav-like warning and any disk-save message so all advisories
// that apply to a response can coexist in Message.
func buildHTMLResult(inURL string, resp *fetch.Response, workDir string, maxSize int, fallbackMessage string) (any, error) {
	if resp.StatusCode != 200 {
		return newFetchURLHTTPError(inURL, resp), nil
	}

	content := resp.Markdown
	data := []byte(content)
	truncated := len(data) > maxSize
	if truncated {
		data = data[:maxSize]
		data = trimIncompleteUTF8Suffix(data)
	}
	content = string(data)
	contentLength := utf8.RuneCountInString(content)

	advisory := fallbackMessage
	if looksLikeNavigation(content) {
		advisory = appendAdvisory(advisory, navigationAdvisoryMessage)
	}

	if contentLength <= inlineThreshold {
		msg := advisory
		if truncated {
			msg = appendAdvisory(msg, truncationAdvisory(maxSize))
		}
		return &FetchURLResult{
			URL:           resp.URL,
			Title:         resp.Metadata.Title,
			Description:   resp.Metadata.Description,
			Content:       content,
			ContentLength: contentLength,
			StatusCode:    resp.StatusCode,
			Message:       msg,
		}, nil
	}

	result, err := saveFetchedContent(workDir, content, "text/html", truncated, maxSize)
	if err != nil {
		return nil, fmt.Errorf("fetch_url: %w", err)
	}
	result.URL = resp.URL
	result.Title = resp.Metadata.Title
	result.Description = resp.Metadata.Description
	result.StatusCode = resp.StatusCode
	result.Message = appendAdvisory(advisory, result.Message)
	return result, nil
}
