package host

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/netseven/infra-probe/internal/redact"
)

// CronEntry is a scheduled job.
type CronEntry struct {
	// Source is the file the entry comes from: /etc/crontab, a file of
	// /etc/cron.d, a user crontab, or a /etc/cron.<period> directory.
	Source   string `json:"source"`
	Schedule string `json:"schedule"` // "*/5 * * * *", "@daily", or hourly/daily/weekly/monthly
	User     string `json:"user"`
	Command  string `json:"command"` // secrets in arguments are masked
}

// envAssignment matches the NAME=value lines of a crontab, which set
// variables for the jobs and are not jobs (and may hold secrets).
var envAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*=`)

// periodicDirs are run by cron at the given frequency through run-parts.
var periodicDirs = []string{"hourly", "daily", "weekly", "monthly"}

// userCrontabDirs are where cron keeps the crontab of each user: Debian and
// Red Hat use different directories.
var userCrontabDirs = []string{"var/spool/cron/crontabs", "var/spool/cron"}

func (c *Collector) collectCron() ([]CronEntry, []string, error) {
	var (
		entries []CronEntry
		notes   []string
		errs    []error
	)
	add := func(source string, data []byte, user string) {
		entries = append(entries, parseCrontab(data, source, user)...)
	}

	// System crontab and /etc/cron.d: they carry the user in each line.
	if data, err := c.readFile("etc/crontab"); err == nil {
		add("/etc/crontab", data, "")
	} else if !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("cron: /etc/crontab: %w", err))
	}
	files, err := fs.ReadDir(c.fsys, "etc/cron.d")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("cron: /etc/cron.d: %w", err))
	}
	for _, f := range files {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		data, err := c.readFile("etc/cron.d/" + f.Name())
		if err != nil {
			errs = append(errs, fmt.Errorf("cron: /etc/cron.d/%s: %w", f.Name(), err))
			continue
		}
		add("/etc/cron.d/"+f.Name(), data, "")
	}

	// Scripts run hourly, daily... are listed by name: their content is not read.
	for _, period := range periodicDirs {
		dir := "etc/cron." + period
		scripts, err := fs.ReadDir(c.fsys, dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("cron: /%s: %w", dir, err))
			}
			continue
		}
		for _, s := range scripts {
			if s.IsDir() || strings.HasPrefix(s.Name(), ".") {
				continue
			}
			entries = append(entries, CronEntry{Source: "/" + dir, Schedule: period, User: "root", Command: "/" + dir + "/" + s.Name()})
		}
	}

	// Crontabs of the users: readable by root only.
	for _, dir := range userCrontabDirs {
		files, err := fs.ReadDir(c.fsys, dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if errors.Is(err, fs.ErrPermission) {
			notes = append(notes, "The crontabs of the users are not readable by the probe. Run it as root to include them.")
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("cron: /%s: %w", dir, err))
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			data, err := c.readFile(dir + "/" + f.Name())
			if err != nil {
				errs = append(errs, fmt.Errorf("cron: /%s/%s: %w", dir, f.Name(), err))
				continue
			}
			add("/"+dir+"/"+f.Name(), data, f.Name())
		}
	}

	if entries == nil {
		entries = []CronEntry{}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Source < entries[j].Source })
	return entries, notes, errors.Join(errs...)
}

// parseCrontab parses the jobs of a crontab. A line is "m h dom mon dow
// command", or "@daily command"; system crontabs carry the user as an extra
// field before the command (pass user == ""). Comments, blank lines and
// variable assignments are skipped.
func parseCrontab(data []byte, source, user string) []CronEntry {
	var out []CronEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || envAssignment.MatchString(line) {
			continue
		}
		fields := strings.Fields(line)

		var schedule string
		if strings.HasPrefix(fields[0], "@") {
			schedule, fields = fields[0], fields[1:]
		} else if len(fields) >= 5 {
			schedule, fields = strings.Join(fields[:5], " "), fields[5:]
		} else {
			continue
		}

		jobUser := user
		if user == "" {
			if len(fields) < 2 {
				continue
			}
			jobUser, fields = fields[0], fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		out = append(out, CronEntry{
			Source: source, Schedule: schedule, User: jobUser,
			Command: redact.Command(strings.Join(fields, " ")),
		})
	}
	return out
}
