package prompt

import (
	"context"
)

// Assemble renders the request prompt from static context and conversation state.
func Assemble(ctx context.Context, opts AssemblyOptions) (Assembly, error) {
	if err := validateAssemblyOptions(opts); err != nil {
		return Assembly{}, err
	}
	assembler, err := newAssembler(opts)
	if err != nil {
		return Assembly{}, err
	}
	return assembler.Assemble(ctx)
}
