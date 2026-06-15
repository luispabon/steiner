active branch: cl/2026-06-15_system_prompt_narrative

verification strategy:
- gofmt/goimports on touched Go files
- targeted package checks from plan.yaml after relevant steps
- go test ./...
- go vet ./...
- golangci-lint run ./...
- make check before handoff

planning artifacts:
- .project_planning/ is version-controlled

steps:
- step-1: complete
- step-2: complete
- step-3: complete
- step-4: complete
- step-5: complete

sub-agents:
- step-1: worker Popper (019ecb99-6cd8-7953-bdc8-062877db777c), model gpt-5.5 medium, escalation/override because user required smart thinking model for system-preamble wording changes
- step-3: worker Ramanujan (019ecb99-dab7-7603-af58-52927e911acf), model gpt-5.4-mini medium, cheaper than current runtime; no planner delegate_profile present
- step-2: worker Jason (019ecb9d-94c4-7893-ae4c-2783784e9c92), model gpt-5.4-mini medium, cheaper than current runtime; no planner delegate_profile present
- step-4: worker Copernicus (019ecb9d-c55b-7f00-85cc-4f3c390cee0f), model gpt-5.4-mini medium, cheaper than current runtime; no planner delegate_profile present

verification:
- step-1 worker: gofmt, goimports, go build ./internal/prompt/, go test ./internal/prompt/, make check passed
- step-3 worker: gofmt, goimports, go build ./internal/delegation/, go test ./internal/delegation/ -v -run TestSpecialized passed; go test ./internal/delegation/ -v -run TestBuildChild failed on stale test expectation to update in step-4
- step-2 worker: gofmt, goimports, go test ./internal/prompt/ -v -run TestSystemPreamble, go test ./internal/prompt/ -v -run TestBuildConversation, go test ./internal/prompt/ -v passed
- step-4 worker: gofmt, goimports attempted, go test ./internal/delegation/ -v passed
- executor targeted: go test ./internal/prompt/ ./internal/delegation/ ./internal/agent/ passed
- executor broad: go test ./... passed
- executor vet: go vet ./... passed
- executor lint: golangci-lint run ./... passed with 0 issues
- executor final: make check passed, including go mod tidy/diff, build, go test ./..., go test -race ./..., go vet ./..., golangci-lint run ./..., govulncheck ./...

deviations/blockers/manual verification:
- user requested gpt-5.5 medium for steps that tweak wording in the system preamble
- no blockers
- no manual verification required by plan

reviewer handoff:
- ready; feature branch clean after executor-state commit
