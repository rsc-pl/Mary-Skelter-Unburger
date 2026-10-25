package maintainer

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type SourcePaths struct {
	MS2Game      string
	FinaleGame   string
	MS2Master    string
	FinaleMaster string
	MS2Exe       string
	FinaleExe    string
	MS2Switch    string
}

func sourceConfigPath(root string) string {
	return filepath.Join(root, ".local", "source_paths.sh")
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// parseShellValue parses the deliberately tiny shell-word subset used by
// .local/source_paths.sh. It also accepts historical `printf %q` output so old
// maintainer configurations can be migrated without invoking a shell.
func parseShellValue(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "$'") && strings.HasSuffix(s, "'") {
		inner := s[2 : len(s)-1]
		q, err := strconv.Unquote(`"` + strings.ReplaceAll(inner, `"`, `\"`) + `"`)
		if err != nil {
			return "", fmt.Errorf("decode ANSI-C shell word: %w", err)
		}
		return q, nil
	}
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == 0 {
			switch c {
			case '\\':
				i++
				if i >= len(s) {
					return "", fmt.Errorf("trailing shell escape")
				}
				b.WriteByte(s[i])
			case '\'', '"':
				quote = c
			default:
				b.WriteByte(c)
			}
			continue
		}
		if c == quote {
			quote = 0
			continue
		}
		if quote == '"' && c == '\\' {
			i++
			if i >= len(s) {
				return "", fmt.Errorf("trailing double-quote escape")
			}
			b.WriteByte(s[i])
			continue
		}
		if quote == '\'' && c == '\\' && i+3 < len(s) && s[i+1] == '\'' && s[i+2] == '\\' && s[i+3] == '\'' {
			b.WriteByte('\'')
			i += 3
			continue
		}
		b.WriteByte(c)
	}
	if quote != 0 {
		return "", fmt.Errorf("unterminated shell quote")
	}
	return b.String(), nil
}

func ReadSourcePaths(root string) (SourcePaths, error) {
	p := sourceConfigPath(root)
	f, err := os.Open(p)
	if err != nil {
		return SourcePaths{}, fmt.Errorf("read %s: %w", p, err)
	}
	defer f.Close()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value, err := parseShellValue(v)
		if err != nil {
			return SourcePaths{}, fmt.Errorf("%s: parse %s: %w", p, strings.TrimSpace(k), err)
		}
		vals[strings.TrimSpace(k)] = value
	}
	if err := sc.Err(); err != nil {
		return SourcePaths{}, err
	}
	out := SourcePaths{
		MS2Game: vals["MS2_PC_GAME"], FinaleGame: vals["MSF_PC_GAME"],
		MS2Master: vals["MS2_PC_MASTER"], FinaleMaster: vals["MSF_PC_MASTER"],
		MS2Exe: vals["MS2_PC_EXE"], FinaleExe: vals["MSF_PC_EXE"], MS2Switch: vals["MS2_SWITCH_MASTER"],
	}
	if out.FinaleGame == "" {
		out.FinaleGame = vals["FINALE_PC_GAME"]
	}
	if out.FinaleExe == "" {
		out.FinaleExe = vals["FINALE_PC_EXE"]
	}
	if out.MS2Game == "" || out.FinaleGame == "" {
		return out, fmt.Errorf("source_paths.sh is missing MS2_PC_GAME or MSF_PC_GAME")
	}
	if out.MS2Master == "" {
		out.MS2Master = filepath.Join(out.MS2Game, "masterWindows")
	}
	if out.FinaleMaster == "" {
		out.FinaleMaster = filepath.Join(out.FinaleGame, "masterWindows")
	}
	if out.MS2Exe == "" {
		out.MS2Exe = filepath.Join(out.MS2Game, "MarySkelter2.exe")
	}
	if out.FinaleExe == "" {
		candidate := filepath.Join(out.FinaleGame, "MarySkelterFinale.exe")
		if file(candidate) {
			out.FinaleExe = candidate
		}
	}
	return out, nil
}

func ConfigureSources(root, ms2, finale string, stdout, _ io.Writer) error {
	if ms2 == "" || finale == "" {
		return fmt.Errorf("both MS2 and Finale game directories are required")
	}
	var err error
	if ms2, err = filepath.Abs(ms2); err != nil {
		return err
	}
	if finale, err = filepath.Abs(finale); err != nil {
		return err
	}
	required := []string{
		filepath.Join(ms2, "masterWindows"), filepath.Join(finale, "masterWindows"), filepath.Join(ms2, "MarySkelter2.exe"),
	}
	for i, p := range required {
		st, e := os.Stat(p)
		if e != nil || (i < 2 && !st.IsDir()) || (i == 2 && !st.Mode().IsRegular()) {
			return fmt.Errorf("required source path not found: %s", p)
		}
	}
	finaleExe := filepath.Join(finale, "MarySkelterFinale.exe")
	lines := []string{
		"export MS2_PC_GAME=" + shellSingleQuote(ms2),
		"export MSF_PC_GAME=" + shellSingleQuote(finale),
		"export FINALE_PC_GAME=" + shellSingleQuote(finale),
		"export MS2_PC_MASTER=" + shellSingleQuote(filepath.Join(ms2, "masterWindows")),
		"export MSF_PC_MASTER=" + shellSingleQuote(filepath.Join(finale, "masterWindows")),
		"export MS2_PC_EXE=" + shellSingleQuote(filepath.Join(ms2, "MarySkelter2.exe")),
	}
	if file(finaleExe) {
		lines = append(lines,
			"export MSF_PC_EXE="+shellSingleQuote(finaleExe),
			"export FINALE_PC_EXE="+shellSingleQuote(finaleExe),
		)
	}
	p := sourceConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Wrote .local/source_paths.sh (native Go)\n")
	}
	return nil
}
