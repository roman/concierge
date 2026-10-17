package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/roman/concierge/pkg/herdr"
)

var ErrFocusUnsafe = errors.New("pane close would change a live viewer's focus")

type focus struct {
	Workspace string `json:"workspace"`
	Tab       string `json:"tab"`
}

// CloseBoundPane closes one exact pane and restores the viewer's prior tab.
// The caller must hold the task binding lock until this operation returns.
func CloseBoundPane(
	ctx context.Context, client herdr.Client, target herdr.Target, workspaceID, tabID string,
	checkBinding func() error,
) error {
	return closeBoundPane(
		ctx,
		client,
		target,
		workspaceID,
		tabID,
		checkBinding,
		presentationNamespace,
	)
}

func closeBoundPane(
	ctx context.Context, client herdr.Client, target herdr.Target, workspaceID, tabID string,
	checkBinding func() error, namespace string,
) error {
	if target.Session() == "" || target.PaneID() == "" || workspaceID == "" || tabID == "" ||
		checkBinding == nil {
		return herdr.ErrResponse
	}
	bound, err := client.ForEndpoint(target.Machine(), target.Session())
	if err != nil {
		return err
	}
	client = bound
	return withLockSocket(ctx, client, target.Session(), namespace, func(socket string) error {
		return closeLocked(ctx, client, target, workspaceID, tabID, checkBinding, socket, namespace)
	})
}

func closeLocked(
	ctx context.Context, client herdr.Client, target herdr.Target, workspaceID, tabID string,
	checkBinding func() error, socket, namespace string,
) error {
	if err := checkBinding(); err != nil {
		return err
	}
	path := journalPath(namespace, target, socket)
	journal, selected, err := readCloseJournal(path, target, socket, workspaceID, tabID)
	if err == nil {
		if err := recoverCloseJournal(ctx, client, target, path, journal, selected); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := client.ConfirmPaneGone(ctx, target); err == nil {
		return nil
	} else if !errors.Is(err, herdr.ErrPanePresent) {
		return err
	}
	if err := checkPaneLocation(ctx, client, target, workspaceID, tabID); err != nil {
		return err
	}
	if err := checkBinding(); err != nil {
		return err
	}
	before, err := readFocus(ctx, client, target.Session())
	if err != nil {
		return err
	}
	journal = closeJournal{
		Session: target.Session(), Pane: target.PaneID(), Socket: socket,
		Workspace: workspaceID, Tab: tabID, Focus: before,
	}
	selected, err = publishCloseJournal(path, journal)
	if err != nil {
		return err
	}
	if err := checkPaneLocation(ctx, client, target, workspaceID, tabID); err != nil {
		return errors.Join(err, clearCloseJournal(path, selected))
	}
	if err := checkBinding(); err != nil {
		return errors.Join(err, clearCloseJournal(path, selected))
	}
	fresh, err := readFocus(ctx, client, target.Session())
	if err != nil {
		return errors.Join(err, clearCloseJournal(path, selected))
	}
	if fresh != before {
		return errors.Join(ErrFocusUnsafe, clearCloseJournal(path, selected))
	}
	if before.Tab == tabID {
		viewer, err := readViewer(ctx, client, target.Session())
		if err != nil {
			return errors.Join(err, clearCloseJournal(path, selected))
		}
		if viewer != "no_foreground_client" {
			return errors.Join(ErrFocusUnsafe, clearCloseJournal(path, selected))
		}
	}
	_, closeErr := runResult(ctx, client, target.Session(), "pane", "close", target.PaneID())
	confirmErr := client.ConfirmPaneGone(ctx, target)
	if confirmErr == nil {
		closeErr = nil
	}
	var restoreErr error
	if journal.Focus.Tab != journal.Tab {
		restoreErr = restoreFocus(ctx, client, target.Session(), before)
	}
	if confirmErr == nil || errors.Is(confirmErr, herdr.ErrPanePresent) {
		if restoreErr == nil {
			restoreErr = clearCloseJournal(path, selected)
		}
	}
	return errors.Join(closeErr, confirmErr, restoreErr)
}

func recoverCloseJournal(
	ctx context.Context, client herdr.Client, target herdr.Target, path string,
	journal closeJournal, selected os.FileInfo,
) error {
	presence := client.ConfirmPaneGone(ctx, target)
	if errors.Is(presence, herdr.ErrPanePresent) {
		return clearCloseJournal(path, selected)
	}
	if presence != nil {
		return presence
	}
	if journal.Focus.Tab != journal.Tab {
		current, err := readFocus(ctx, client, target.Session())
		if err != nil {
			return err
		}
		if current != journal.Focus {
			viewer, err := readViewer(ctx, client, target.Session())
			if err != nil {
				return err
			}
			if viewer != "no_foreground_client" {
				return ErrFocusUnsafe
			}
			if err := restoreFocus(ctx, client, target.Session(), journal.Focus); err != nil {
				return err
			}
		}
	}
	return clearCloseJournal(path, selected)
}

func checkPaneLocation(
	ctx context.Context, client herdr.Client, target herdr.Target, workspaceID, tabID string,
) error {
	bound, err := client.ForEndpoint(target.Machine(), target.Session())
	if err != nil {
		return err
	}
	details, err := bound.PaneDetails(ctx, target.PaneID())
	if err != nil {
		return err
	}
	if details.WorkspaceID != workspaceID || details.TabID != tabID {
		return fmt.Errorf("%w: recorded pane location changed", herdr.ErrResponse)
	}
	return nil
}

func runResult(
	ctx context.Context, client herdr.Client, session string, args ...string,
) (json.RawMessage, error) {
	stdout, stderr, err := client.RunScoped(ctx, session, args...)
	return commandResult(stdout, stderr, err, args[0])
}

func commandResult(stdout, stderr []byte, err error, operation string) (json.RawMessage, error) {
	if err != nil {
		return nil, fmt.Errorf("%w: %s failed: %w", herdr.ErrCommand, operation, err)
	}
	if len(stderr) != 0 {
		return nil, fmt.Errorf("%w: %s returned diagnostics", herdr.ErrCommand, operation)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(stdout, &response) != nil || response.Error.Code != "" ||
		len(response.Result) == 0 {
		return nil, fmt.Errorf("%w: ambiguous %s response", herdr.ErrResponse, operation)
	}
	return response.Result, nil
}

func readFocus(ctx context.Context, client herdr.Client, session string) (focus, error) {
	result, err := runResult(ctx, client, session, "workspace", "list")
	if err != nil {
		return focus{}, err
	}
	var workspaces struct {
		Workspaces []struct {
			ID        string `json:"workspace_id"`
			ActiveTab string `json:"active_tab_id"`
			Focused   bool   `json:"focused"`
		} `json:"workspaces"`
	}
	if json.Unmarshal(result, &workspaces) != nil {
		return focus{}, herdr.ErrResponse
	}
	var selected focus
	count := 0
	for _, workspace := range workspaces.Workspaces {
		if workspace.Focused {
			selected = focus{workspace.ID, workspace.ActiveTab}
			count++
		}
	}
	if count != 1 || selected.Workspace == "" || selected.Tab == "" {
		return focus{}, herdr.ErrResponse
	}
	result, err = runResult(ctx, client, session, "tab", "list", "--workspace", selected.Workspace)
	if err != nil {
		return focus{}, err
	}
	var tabs struct {
		Tabs []struct {
			ID      string `json:"tab_id"`
			Focused bool   `json:"focused"`
		} `json:"tabs"`
	}
	if json.Unmarshal(result, &tabs) != nil {
		return focus{}, herdr.ErrResponse
	}
	count = 0
	for _, tab := range tabs.Tabs {
		if tab.Focused {
			if tab.ID != selected.Tab {
				return focus{}, herdr.ErrResponse
			}
			count++
		}
	}
	if count != 1 {
		return focus{}, herdr.ErrResponse
	}
	return selected, nil
}

func readViewer(ctx context.Context, client herdr.Client, session string) (string, error) {
	stdout, stderr, checkError := client.RunPresentationCheck(ctx, session, herdr.ForegroundViewer)
	result, err := commandResult(stdout, stderr, checkError, "terminal")
	if err != nil {
		return "", err
	}
	var viewer struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(result, &viewer) != nil ||
		(viewer.Reason != "cleared" && viewer.Reason != "no_foreground_client") {
		return "", herdr.ErrResponse
	}
	return viewer.Reason, nil
}

func restoreFocus(ctx context.Context, client herdr.Client, session string, before focus) error {
	current, err := readFocus(ctx, client, session)
	if err == nil && current == before {
		return nil
	}
	result, err := runResult(ctx, client, session, "tab", "get", before.Tab)
	if err != nil {
		return fmt.Errorf("focus restore: %w", err)
	}
	var tab struct {
		Tab struct {
			ID        string `json:"tab_id"`
			Workspace string `json:"workspace_id"`
		} `json:"tab"`
	}
	if json.Unmarshal(result, &tab) != nil || tab.Tab.ID != before.Tab ||
		tab.Tab.Workspace != before.Workspace {
		return fmt.Errorf("focus restore: %w", herdr.ErrResponse)
	}
	if _, err := runResult(ctx, client, session, "tab", "focus", before.Tab); err != nil {
		return fmt.Errorf("focus restore: %w", err)
	}
	current, err = readFocus(ctx, client, session)
	if err != nil || current != before {
		return fmt.Errorf("focus restore: %w", herdr.ErrResponse)
	}
	return nil
}
