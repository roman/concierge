package machineprep

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func preparationFailure(ctx context.Context, phase string, diagnostics []byte, cause error) error {
	if ctx.Err() != nil {
		cause = errors.Join(cause, ctx.Err())
	}
	if cause == nil {
		cause = errors.New("unexpected completion response")
	}
	const tailBytes = 4096
	if len(diagnostics) > tailBytes {
		diagnostics = diagnostics[len(diagnostics)-tailBytes:]
	}
	return fmt.Errorf("%s: %w: %w; stderr tail: %s", phase,
		ErrPreparationUnconfirmed, cause, strings.TrimSpace(string(diagnostics)))
}
