package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const marker = ".runnerd-owned"
const markerValue = "runnerd-workspace-v1\n"

type Workspaces struct{ Home, State string }

func NewWorkspaces(home, state string) (*Workspaces, error) {
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve runner template: %w", err)
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return nil, fmt.Errorf("create runner state directory: %w", err)
	}
	state, err = filepath.EvalSymlinks(state)
	if err != nil {
		return nil, fmt.Errorf("resolve runner state directory: %w", err)
	}
	for _, pair := range [][2]string{{home, state}, {state, home}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("runner template and state directory overlap after resolving symlinks")
		}
	}
	if err := os.Chmod(state, 0700); err != nil {
		return nil, fmt.Errorf("secure runner state directory: %w", err)
	}
	w := &Workspaces{Home: home, State: state}
	return w, w.Ready()
}

func (w *Workspaces) Ready() error {
	for _, path := range []string{"run.sh", "bin/Runner.Listener"} {
		info, err := os.Stat(filepath.Join(w.Home, filepath.FromSlash(path)))
		if err != nil {
			return fmt.Errorf("runner template missing %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("runner template %s must be a regular file", path)
		}
	}
	for _, path := range []string{".runner", ".credentials", ".credentials_rsaparams", ".env", ".path", "_work", "_diag"} {
		if _, err := os.Lstat(filepath.Join(w.Home, path)); err == nil {
			return fmt.Errorf("runner template contains %s; use a pristine, unconfigured installation", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check pristine runner template: %w", err)
		}
	}
	f, err := os.CreateTemp(w.State, ".write-check-")
	if err != nil {
		return fmt.Errorf("runner state directory not writable: %w", err)
	}
	path := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(path)
	return errors.Join(closeErr, removeErr)
}

func (w *Workspaces) Create(ctx context.Context) (path string, err error) {
	path, err = os.MkdirTemp(w.State, "instance-")
	if err != nil {
		return "", fmt.Errorf("create isolated runner workspace: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(path))
		}
	}()
	if err = os.WriteFile(filepath.Join(path, marker), []byte(markerValue), 0600); err != nil {
		return path, fmt.Errorf("mark workspace: %w", err)
	}
	err = filepath.WalkDir(w.Home, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(w.Home, source)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == marker || rel == ".active" {
			return fmt.Errorf("runner template contains reserved workspace metadata")
		}
		dest := filepath.Join(path, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(source)
			if err != nil {
				return err
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(rel), target))
			if filepath.IsAbs(target) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("template symlink escapes installation: %s", rel)
			}
			return os.Symlink(target, dest)
		}
		if entry.IsDir() {
			return os.Mkdir(dest, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported template file type: %s", rel)
		}
		return copyFile(ctx, source, dest, info.Mode().Perm()&0700)
	})
	if err != nil {
		return path, fmt.Errorf("copy runner template: %w", err)
	}
	return path, nil
}

func copyFile(ctx context.Context, source, dest string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, &contextReader{ctx: ctx, reader: in})
	return errors.Join(copyErr, out.Close())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

func (w *Workspaces) Cleanup(path string) error {
	if filepath.Dir(filepath.Clean(path)) != w.State || !strings.HasPrefix(filepath.Base(path), "instance-") {
		return fmt.Errorf("refusing cleanup outside owned runner workspace")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing cleanup of non-directory workspace")
	}
	b, err := os.ReadFile(filepath.Join(path, marker))
	if err != nil || string(b) != markerValue {
		return fmt.Errorf("refusing cleanup of unmarked workspace")
	}
	err = filepath.WalkDir(path, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return os.Chmod(p, 0700)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("prepare workspace cleanup: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove runner workspace: %w", err)
	}
	return nil
}

func (w *Workspaces) CleanupStale(log *slog.Logger) error {
	entries, err := os.ReadDir(w.State)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "instance-") {
			continue
		}
		path := filepath.Join(w.State, entry.Name())
		owned, err := os.ReadFile(filepath.Join(path, marker))
		if err != nil || string(owned) != markerValue {
			continue
		}
		active, err := os.ReadFile(filepath.Join(path, ".active"))
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(active)))
			if parseErr != nil || pid <= 1 || processGroupAlive(pid) {
				log.Warn("stale workspace preserved; inspect before removal", "workspace", path)
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect stale runner: %w", err)
		}
		if err := w.Cleanup(path); err != nil {
			return err
		}
		log.Info("stale workspace cleaned", "workspace", path)
	}
	return nil
}
