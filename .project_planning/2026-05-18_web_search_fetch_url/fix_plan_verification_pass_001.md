# Fix Plan: Verification Pass 001

## Failures from `make check`

### revive: unused-parameter (8)
1. `internal/tool/builtin/fetch_url.go:31` — `env Env` param unused → rename to `_ Env`
2. `internal/tool/builtin/fetch_url_test.go:25` — `r *http.Request` unused → rename to `_ *http.Request`
3. `internal/tool/builtin/fetch_url_test.go:107` — same
4. `internal/tool/builtin/search_backend_test.go:112` — same
5. `internal/tool/builtin/web_search_test.go:17` — `ctx context.Context` unused → rename to `_ context.Context`
6. Others from revive count (total 8; check `make check` output for full list)

### errcheck (8)
1. `brave_search.go:77` — `resp.Body.Close` unchecked → `defer func() { _ = resp.Body.Close() }()`
2. `brave_search_test.go:139` — `w.Write` unchecked → `_, _ = w.Write(...)`
3. `brave_search_test.go:193` — same
4. `fetch_url_test.go:27` — `fmt.Fprint` unchecked → `_, _ = fmt.Fprint(...)`
5. `fetch_url_test.go:109` — same
6. `search_backend.go:98` — `resp.Body.Close` unchecked → `defer func() { _ = resp.Body.Close() }()`
7. `search_backend_test.go:128` — `json.Encoder.Encode` unchecked → `_ = json.NewEncoder(w).Encode(...)`
8. `search_backend_test.go:163` — `w.Write` unchecked → `_, _ = w.Write(...)`

### goimports (3)
1. `fetch_url_test.go:12` — run `goimports -w`
2. `search_backend_test.go:10` — run `goimports -w`
3. `web_search.go:7` — run `goimports -w`

## Files to touch
- internal/tool/builtin/fetch_url.go
- internal/tool/builtin/fetch_url_test.go
- internal/tool/builtin/brave_search.go
- internal/tool/builtin/brave_search_test.go
- internal/tool/builtin/search_backend.go
- internal/tool/builtin/search_backend_test.go
- internal/tool/builtin/web_search.go
- internal/tool/builtin/web_search_test.go
