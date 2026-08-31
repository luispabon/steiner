package agent

// ParallelClass groups tool calls that may run concurrently with siblings of
// the same class within one assistant turn. A batch of adjacent calls only
// forms one concurrent run when every call shares the same class — a class
// change breaks the run exactly like a ParallelClassNone call does, so e.g.
// [grep, delegate, grep] never lets a delegation spawn and a grep compete
// for slots in the same semaphore.
type ParallelClass int

const (
	// ParallelClassNone marks a call that must run serially with respect to
	// its siblings. The zero value, so an unset classifier or an
	// unrecognized tool name defaults to serial.
	ParallelClassNone ParallelClass = iota
	// ParallelClassDelegation marks a delegation tool call (a specialized
	// sub-agent spawn, or follow_up), bounded by RunRequest.MaxParallelDelegations.
	ParallelClassDelegation
	// ParallelClassTool marks a parallel-safe non-delegation tool call
	// (read, glob, grep, ls, fetch_url, web_search, MCP tools flagged
	// parallel-safe, ...), bounded by RunRequest.MaxParallelTools.
	ParallelClassTool
)
