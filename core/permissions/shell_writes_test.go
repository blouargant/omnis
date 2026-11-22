package permissions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestShellWriteTargetsForms(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cat > out.md <<'EOF'\nhello\nEOF", []string{"/work/out.md"}},
		{"echo x >> /srv/a.txt", []string{"/srv/a.txt"}},
		{"cmd &> all.log", []string{"/work/all.log"}},
		{"cmd 2>&1 | tee -a build.log", []string{"/work/build.log"}},
		{"cmd > /dev/null 2>&1", nil},
		{"echo x >&2", nil},
		{"sed -i 's/a/b/' src/a.go src/b.go", []string{"/work/src/a.go", "/work/src/b.go"}},
		{"sed -i -e 's/a/b/' -e 's/c/d/' x.py", []string{"/work/x.py"}},
		{"sed -n '1,5p' x.py", nil},
		{"perl -pi -e 's/a/b/' x.pl", []string{"/work/x.pl"}},
		{"cp /tmp/draft.md report.md", []string{"/work/report.md"}},
		{"mv a.txt b.txt", []string{"/work/b.txt"}},
		{"pandoc notes.md -o /tmp/report.docx", []string{"/tmp/report.docx"}},
		{"curl -sL https://x/y --output=data.json", []string{"/work/data.json"}},
		{"wget -q -O page.html https://x", []string{"/work/page.html"}},
		{"zip -r out.zip src", []string{"/work/out.zip"}},
		{"tar -czf /tmp/a.tgz src", []string{"/tmp/a.tgz"}},
		{"tar -xzf a.tgz", nil},
		{"cd /app && echo x > a.txt", []string{"/app/a.txt"}},
		{"cd sub; echo x > a.txt", []string{"/work/sub/a.txt"}},
		{"cd $DIR && echo x > a.txt", nil},
		{"echo x > $OUT", nil},
		{"echo x > out-*.txt", nil},
		{"echo x > \"my report.md\"", []string{"/work/my report.md"}},
		{"awk '$1 > 3 {print}' data.csv", nil},
		{"grep 'a > b' f.txt", nil},
		{"sudo tee /etc/x.conf", []string{"/etc/x.conf"}},
		{"timeout 30 sh -c 'true' > t.txt", []string{"/work/t.txt"}},
		{"echo a > f.txt; echo b >> f.txt", []string{"/work/f.txt"}},
		// Extra cases from the Harbor traces and edge shapes.
		{"cd /app && sed -i 's/old/new/g' src/main.rs && cargo build 2>&1 | tail -20", []string{"/app/src/main.rs"}},
		{"cmd | cd /x; echo y > z.txt", []string{"/work/z.txt"}},
		{"sort -o sorted.txt in.txt", []string{"/work/sorted.txt"}},
		{"curl -sLo f.bin https://x", []string{"/work/f.bin"}},
		{"echo x > /proc/self/fd/1", nil},
		{"install -m 0755 build/omnis /usr/local/bin/omnis", []string{"/usr/local/bin/omnis"}},
		{"tar czf out.tar.gz dir", []string{"/work/out.tar.gz"}},
		{"zip archive src", []string{"/work/archive.zip"}},
	}
	for _, c := range cases {
		got := ShellWriteTargets(c.cmd, "/work")
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ShellWriteTargets(%q) = %#v, want %#v", c.cmd, got, c.want)
		}
	}
}

func TestShellWriteTargetsCap(t *testing.T) {
	var parts []string
	for i := 0; i < 20; i++ {
		parts = append(parts, "echo x > f"+string(rune('a'+i))+".txt")
	}
	got := ShellWriteTargets(strings.Join(parts, "; "), "/work")
	if len(got) != 16 {
		t.Fatalf("got %d targets, want 16: %v", len(got), got)
	}
	if got[0] != "/work/fa.txt" {
		t.Fatalf("first target %q", got[0])
	}
}

func TestShellWriteTargetsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	got := ShellWriteTargets("echo x > ~/n.txt", "/work")
	want := []string{filepath.Join(home, "n.txt")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestShellWriteTargetsCpIntoDir(t *testing.T) {
	dir := t.TempDir()
	got := ShellWriteTargets("cp /tmp/draft.md "+dir, "/work")
	want := []string{filepath.Join(dir, "draft.md")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = ShellWriteTargets("mv a.md b.md "+dir+"/", "/work")
	want = []string{filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestShellWriteTargetsEmptyCwd(t *testing.T) {
	got := ShellWriteTargets("echo x > rel.txt; echo y > /abs/a.txt", "")
	want := []string{"/abs/a.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
