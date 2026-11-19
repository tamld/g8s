package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SensitiveDenySpec returns canonicalized credential directories and sensitive
// files (including F0 private signing keys and agent sockets) that sandboxed
// workers must never read, while keeping f0-signing.pub and receipts.db readable.
func SensitiveDenySpec(homeDir, stateDir, sshAuthSock string) (denyDirs []string, denyFiles []string) {
	seenDir := make(map[string]bool)
	addDir := func(p string) {
		c := CanonicalPath(p)
		if c != "" && !seenDir[c] {
			seenDir[c] = true
			denyDirs = append(denyDirs, c)
		}
	}
	seenFile := make(map[string]bool)
	addFile := func(p string) {
		c := CanonicalPath(p)
		if c != "" && !seenFile[c] {
			seenFile[c] = true
			denyFiles = append(denyFiles, c)
		}
	}

	if cleanHome := strings.TrimSpace(homeDir); cleanHome != "" {
		for _, sub := range []string{
			".ssh",
			".aws",
			".gnupg",
			".kube",
			".docker",
			filepath.Join(".config", "gh"),
			filepath.Join(".config", "gcloud"),
		} {
			addDir(filepath.Join(cleanHome, sub))
		}
		for _, sub := range []string{
			".netrc",
			".npmrc",
			".pypirc",
			".git-credentials",
			filepath.Join(".config", "g8s", "providers.json"),
			filepath.Join(".config", "g8s", ".env"),
		} {
			addFile(filepath.Join(cleanHome, sub))
		}
	}

	if cleanState := strings.TrimSpace(stateDir); cleanState != "" {
		addFile(filepath.Join(cleanState, "f0-signing.key"))
		addFile(filepath.Join(cleanState, "s0-human.key"))
	}
	if cleanSock := strings.TrimSpace(sshAuthSock); cleanSock != "" {
		addFile(cleanSock)
	}

	return denyDirs, denyFiles
}

// WritableDirsForConfig computes the canonicalized deduplicated list of directories
// writable by the sandboxed worker. WorktreeDir and its git admin dir are included
// strictly when Permission is "workspace_write".
func WritableDirsForConfig(cfg Config, tmpDir, gocacheDir string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(p string) {
		c := CanonicalPath(p)
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}

	add(cfg.RunDir)
	add(tmpDir)
	add(gocacheDir)
	for _, extra := range cfg.ExtraWriteDirs {
		add(extra)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Permission), "workspace_write") {
		add(cfg.WorktreeDir)
		if adminDir := ResolveGitWorktreeAdminDir(cfg.WorktreeDir); adminDir != "" {
			add(adminDir)
		}
	}
	return out
}

func isSameOrParent(candidateParent, child string) bool {
	p := filepath.Clean(candidateParent)
	c := filepath.Clean(child)
	if p == c {
		return true
	}
	if p == string(filepath.Separator) {
		return true
	}
	prefix := p + string(filepath.Separator)
	return strings.HasPrefix(c, prefix)
}

func overlapsWritable(denyPath string, writableDirs []string) bool {
	for _, w := range writableDirs {
		if isSameOrParent(denyPath, w) {
			return true
		}
	}
	return false
}

func escapeSBPLString(p string) (string, error) {
	if strings.ContainsAny(p, "\x00\r\n") {
		return "", fmt.Errorf("sandbox: path %q contains forbidden control characters", p)
	}
	escaped := strings.ReplaceAll(p, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return escaped, nil
}

// BuildSeatbeltProfile generates an XNU Seatbelt (SBPL v1) profile denying all
// writes outside writableDirs and device nodes, and denying reads of denyDirs
// and denyFiles.
func BuildSeatbeltProfile(writableDirs, denyDirs, denyFiles []string) (string, error) {
	return BuildSeatbeltProfileWithOptions(writableDirs, denyDirs, denyFiles, false)
}

// BuildSeatbeltProfileWithOptions generates an XNU Seatbelt (SBPL v1) profile
// with optional kernel outbound network denial.
func BuildSeatbeltProfileWithOptions(writableDirs, denyDirs, denyFiles []string, blockOutboundNetwork bool) (string, error) {
	var sb strings.Builder
	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n")
	sb.WriteString("(deny file-write*)\n")
	sb.WriteString("(allow file-write*\n")
	sb.WriteString("  (literal \"/dev/null\")\n")
	sb.WriteString("  (literal \"/dev/zero\")\n")
	sb.WriteString("  (literal \"/dev/tty\")\n")
	sb.WriteString("  (literal \"/dev/dtracehelper\")\n")
	sb.WriteString("  (regex #\"^/dev/fd/\")\n")
	sb.WriteString("  (regex #\"^/dev/ttys\")\n")
	for _, w := range writableDirs {
		esc, err := escapeSBPLString(w)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "  (subpath \"%s\")\n", esc)
	}
	sb.WriteString(")\n")

	var denyClauses []string
	var denyWriteClauses []string
	for _, d := range denyDirs {
		if overlapsWritable(d, writableDirs) {
			continue
		}
		esc, err := escapeSBPLString(d)
		if err != nil {
			return "", err
		}
		denyClauses = append(denyClauses, fmt.Sprintf("  (subpath \"%s\")\n", esc))
		denyWriteClauses = append(denyWriteClauses, fmt.Sprintf("  (subpath \"%s\")\n", esc))
	}
	for _, f := range denyFiles {
		if overlapsWritable(f, writableDirs) {
			continue
		}
		esc, err := escapeSBPLString(f)
		if err != nil {
			return "", err
		}
		denyClauses = append(denyClauses, fmt.Sprintf("  (literal \"%s\")\n", esc))
	}
	if len(denyClauses) > 0 {
		sb.WriteString("(deny file-read*\n")
		for _, clause := range denyClauses {
			sb.WriteString(clause)
		}
		sb.WriteString(")\n")
	}
	if len(denyWriteClauses) > 0 {
		sb.WriteString("(deny file-write*\n")
		for _, clause := range denyWriteClauses {
			sb.WriteString(clause)
		}
		sb.WriteString(")\n")
	}
	if blockOutboundNetwork {
		sb.WriteString("(deny network-outbound)\n")
	}
	return sb.String(), nil
}

// BuildBwrapArgs builds a deterministic bubblewrap command vector that mounts /
// read-only, binds writableDirs read-write, masks existing sensitive dirs/files,
// sets --die-with-parent, and appends `--` before argv.
func BuildBwrapArgs(
	bwrapBin string,
	argv []string,
	worktreeDir string,
	writableDirs, denyDirs, denyFiles []string,
	statFn func(string) (isDir bool, exists bool),
) []string {
	return BuildBwrapArgsWithOptions(bwrapBin, argv, worktreeDir, writableDirs, denyDirs, denyFiles, false, statFn)
}

// BuildBwrapArgsWithOptions builds a bubblewrap command vector with optional
// `--unshare-net` network namespace isolation.
func BuildBwrapArgsWithOptions(
	bwrapBin string,
	argv []string,
	worktreeDir string,
	writableDirs, denyDirs, denyFiles []string,
	blockOutboundNetwork bool,
	statFn func(string) (isDir bool, exists bool),
) []string {
	if statFn == nil {
		statFn = func(p string) (bool, bool) {
			fi, err := os.Stat(p)
			if err != nil {
				return false, false
			}
			if fi.IsDir() {
				return true, true
			}
			if !fi.Mode().IsRegular() {
				return false, false
			}
			return false, true
		}
	}

	args := []string{
		bwrapBin,
		"--die-with-parent",
	}
	if blockOutboundNetwork {
		args = append(args, "--unshare-net")
	}
	args = append(args,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
	)

	for _, w := range writableDirs {
		if isDir, exists := statFn(w); exists && isDir {
			args = append(args, "--bind", w, w)
		}
	}
	for _, d := range denyDirs {
		if overlapsWritable(d, writableDirs) {
			continue
		}
		if isDir, exists := statFn(d); exists && isDir {
			args = append(args, "--tmpfs", d)
		}
	}
	for _, f := range denyFiles {
		if overlapsWritable(f, writableDirs) {
			continue
		}
		if isDir, exists := statFn(f); exists && !isDir {
			args = append(args, "--ro-bind", "/dev/null", f)
		}
	}
	if strings.TrimSpace(worktreeDir) != "" {
		c := CanonicalPath(worktreeDir)
		if isDir, exists := statFn(c); exists && isDir {
			args = append(args, "--chdir", c)
		}
	}
	args = append(args, "--")
	args = append(args, argv...)
	return args
}

// ComputeLandlockReadAllowPaths computes the minimal set of read/execute
// allowlist paths beneath root such that no path in denyDirs or denyFiles (nor
// any subtree of denyDirs) is included.
func ComputeLandlockReadAllowPaths(
	root string,
	denyDirs, denyFiles []string,
	readDir func(string) ([]os.DirEntry, error),
) ([]string, error) {
	if readDir == nil {
		readDir = os.ReadDir
	}
	cleanRoot := filepath.Clean(root)
	deniedSet := make(map[string]bool)
	ancestorSet := make(map[string]bool)

	recordDenied := func(p string) {
		c := filepath.Clean(p)
		if c == "" || c == "." {
			return
		}
		deniedSet[c] = true
		cur := filepath.Dir(c)
		for {
			ancestorSet[cur] = true
			if cur == cleanRoot || cur == "/" || filepath.Dir(cur) == cur {
				break
			}
			cur = filepath.Dir(cur)
		}
	}
	for _, d := range denyDirs {
		recordDenied(d)
	}
	for _, f := range denyFiles {
		recordDenied(f)
	}

	if len(deniedSet) == 0 {
		return []string{cleanRoot}, nil
	}
	if !ancestorSet[cleanRoot] {
		return []string{cleanRoot}, nil
	}

	var allow []string
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := readDir(dir)
		if err != nil {
			return err
		}
		for _, ent := range entries {
			child := filepath.Join(dir, ent.Name())
			if deniedSet[child] {
				continue
			}
			if ancestorSet[child] && ent.IsDir() {
				if werr := walk(child); werr != nil {
					return werr
				}
				continue
			}
			allow = append(allow, child)
		}
		return nil
	}
	if err := walk(cleanRoot); err != nil {
		return nil, err
	}
	sort.Strings(allow)
	return allow, nil
}
