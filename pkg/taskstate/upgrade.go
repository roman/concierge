package taskstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/taskmeta"
)

// UpgradeVersionTwo backs up quiescent local state before enabling remote records.
func UpgradeVersionTwo(ctx context.Context, rootPath, backupPath string) error {
	if _, err := taskmeta.ParseAbsolutePath(backupPath); err != nil {
		return err
	}
	guard, err := stateversion.AcquireUpgrade(ctx, rootPath)
	if err != nil {
		return err
	}
	defer guard.Close()
	root := guard.StateRoot()
	if err := checkUpgradeRecords(ctx, root); err != nil {
		return err
	}
	if _, err := os.Lstat(backupPath); errors.Is(err, os.ErrNotExist) {
		if _, err := publishStateBackup(ctx, root, rootPath, backupPath, 2,
			backupPublication{syncParent: syncDirectory, rename: renameBackupNoReplace}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := verifyUpgradeBackup(ctx, root, rootPath, backupPath); err != nil {
		return err
	}
	return guard.PublishVersion(3)
}

// RecoverVersionTwoUpgrade verifies the original snapshot before finishing its marker.
func RecoverVersionTwoUpgrade(ctx context.Context, rootPath, backupPath string) error {
	if _, err := taskmeta.ParseAbsolutePath(backupPath); err != nil {
		return err
	}
	guard, err := stateversion.AcquireUpgradeRecovery(ctx, rootPath)
	if err != nil {
		return err
	}
	defer guard.Close()
	root := guard.StateRoot()
	if err := checkUpgradeRecords(ctx, root); err != nil {
		return err
	}
	if err := verifyUpgradeBackup(ctx, root, rootPath, backupPath); err != nil {
		return err
	}
	return guard.PublishVersion(3)
}

func checkUpgradeRecords(ctx context.Context, root *os.Root) error {
	tasks, err := ListPinned(ctx, root)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.record.Worker != nil || task.record.LaunchIntent != nil {
			return fmt.Errorf(
				"task %s still has a worker or launch; retire it before upgrading state",
				task.ID(),
			)
		}
	}
	return nil
}

func verifyUpgradeBackup(ctx context.Context, root *os.Root, rootPath, backupPath string) error {
	return verifyStateBackup(ctx, root, rootPath, backupPath, backupVerification{
		version: 2,
		ignoredPath: func(path string) bool {
			return path == ".task-state-version" || path == ".task-state-migration-pending" ||
				strings.HasPrefix(path, ".task-state-version-") ||
				strings.HasPrefix(path, ".task-state-migration-")
		},
	})
}
