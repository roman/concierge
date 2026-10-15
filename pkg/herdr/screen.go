package herdr

import (
	"context"
	"fmt"
)

// VisibleScreen reads bounded current terminal text.
func (client Client) VisibleScreen(ctx context.Context, paneID string) (string, error) {
	if !healthAtom(paneID, true) {
		return "", fmt.Errorf("%w: invalid Herdr pane", ErrResponse)
	}
	data, err := client.shared().Run(
		ctx,
		"agent",
		"read",
		paneID,
		"--source",
		"visible",
		"--lines",
		"80",
		"--format",
		"text",
	)
	if err != nil {
		return "", err
	}
	if len(data) > 64*1024 {
		return "", fmt.Errorf("%w: visible screen exceeds diagnostic limit", ErrResponse)
	}
	return string(data), nil
}
