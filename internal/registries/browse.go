package registries

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxFileSize bounds a single downloaded file when installing.
const MaxFileSize = 10 << 20 // 10 MiB

// InstalledSkill is one skill currently present in the local registry.
type InstalledSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	LinkedIn    []string `json:"linked_in"` // agent names this skill is symlinked into
}

// ListInstalled scans registryDir and returns one entry per installed skill,
// reading metadata from SKILL.md frontmatter. agentSkills maps agent name to
// its explicit skills list; the LinkedIn column lists every agent that names
// this skill (or, for agents with an empty list, all skills are considered
// available so they are not listed individually).
func ListInstalled(registryDir string, agentSkills map[string][]string) ([]InstalledSkill, error) {
	entries, err := os.ReadDir(registryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []InstalledSkill{}, nil
		}
		return nil, err
	}
	out := make([]InstalledSkill, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirName := e.Name()
		data, err := os.ReadFile(filepath.Join(registryDir, dirName, "SKILL.md"))
		if err != nil {
			continue
		}
		fm, _ := ParseFrontmatter(data)
		display := fm.Name
		if display == "" {
			display = dirName
		}
		sk := InstalledSkill{
			Name:        display,
			Description: fm.Description,
			Author:      fm.Author(),
			Tags:        fm.Tags(),
			LinkedIn:    []string{},
		}
		for agentName, skillList := range agentSkills {
			if slices.Contains(skillList, dirName) || slices.Contains(skillList, display) {
				sk.LinkedIn = append(sk.LinkedIn, agentName)
			}
		}
		out = append(out, sk)
	}
	return out, nil
}

// ReadInstalled returns the SKILL.md content of skillName in the local
// registry, or an error when the skill is not installed.
func ReadInstalled(registryDir, skillName string) ([]byte, error) {
	if !SkillNameRe.MatchString(skillName) {
		return nil, fmt.Errorf("skill name %q is not valid", skillName)
	}
	data, err := os.ReadFile(filepath.Join(registryDir, skillName, "SKILL.md"))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("skill %q is not installed in the local registry", skillName)
	}
	return data, err
}

// BrowseSkills discovers all SKILL.md files in a remote registry. Each
// returned SkillInfo is annotated with Installed=true when a directory of
// the same name already exists under registryDir.
func BrowseSkills(ref RepoRef, token, registryDir string) ([]SkillInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	var skills []SkillInfo
	for _, e := range entries {
		if e.Path == "__truncated__" {
			skills = append(skills, SkillInfo{Name: "__truncated__", DirPath: "__truncated__"})
			continue
		}
		if e.Type != "blob" || !strings.HasSuffix(e.Path, "/SKILL.md") {
			continue
		}
		dirPath := strings.TrimSuffix(e.Path, "/SKILL.md")
		if dirPath == "" {
			continue
		}

		slash := strings.LastIndex(dirPath, "/")
		var group, leafDir string
		if slash >= 0 {
			group, leafDir = dirPath[:slash], dirPath[slash+1:]
		} else {
			leafDir = dirPath
		}

		sk := SkillInfo{
			Name:    leafDir,
			DirPath: dirPath,
			Group:   group,
		}

		if registryDir != "" {
			if _, err := os.Stat(filepath.Join(registryDir, leafDir, "SKILL.md")); err == nil {
				sk.Installed = true
			}
		}

		rawBody, status, err := ref.RawFile(e.Path, token)
		if err == nil && status == 200 {
			fm, _ := ParseFrontmatter(rawBody)
			if fm.Name != "" {
				sk.Name = fm.Name
				if registryDir != "" {
					if _, err := os.Stat(filepath.Join(registryDir, fm.Name, "SKILL.md")); err == nil {
						sk.Installed = true
					}
				}
			}
			sk.Description = fm.Description
			sk.Author = fm.Author()
			sk.Tags = fm.Tags()
		}

		skills = append(skills, sk)
	}

	if skills == nil {
		skills = []SkillInfo{}
	}
	return skills, nil
}

// FetchSkillMD returns the SKILL.md content at dirPath inside the registry.
func FetchSkillMD(ref RepoRef, token, dirPath string) ([]byte, error) {
	rawBody, status, err := ref.RawFile(dirPath+"/SKILL.md", token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching SKILL.md", status)
	}
	return rawBody, nil
}

// collectDirFiles filters a recursive tree listing to files under dirPath and
// returns them as InstallableFiles. Name carries the path relative to dirPath
// (preserving subdirectories, e.g. "scripts/helper.sh"); RelPath is the full
// path from the registry root used by RawFile.
func collectDirFiles(entries []TreeEntry, dirPath string) []InstallableFile {
	prefix := dirPath + "/"
	var files []InstallableFile
	for _, e := range entries {
		if e.Type != "blob" || !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		subPath := strings.TrimPrefix(e.Path, prefix)
		if subPath == "" || strings.Contains(subPath, "..") {
			continue
		}
		files = append(files, InstallableFile{Name: subPath, RelPath: e.Path})
	}
	return files
}

// InstallSkill downloads the files of a remote skill at dirPath (relative to
// the registry root) and writes them under registryDir/<skillName>. The skill
// name is taken from the SKILL.md frontmatter when present, otherwise from
// the leaf of dirPath. Subdirectories (scripts/, assets/, references/) are
// preserved. Returns the resolved skill name.
func InstallSkill(ref RepoRef, token, dirPath, registryDir string) (string, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return "", err
	}
	files := collectDirFiles(entries, dirPath)
	if len(files) == 0 {
		return "", fmt.Errorf("no files found in skill directory")
	}

	leafDir := dirPath
	if i := strings.LastIndex(dirPath, "/"); i >= 0 {
		leafDir = dirPath[i+1:]
	}
	skillName := leafDir

	for _, f := range files {
		if f.Name != "SKILL.md" {
			continue
		}
		rawBody, status, err := ref.RawFile(f.RelPath, token)
		if err == nil && status == 200 {
			fm, _ := ParseFrontmatter(rawBody)
			if fm.Name != "" {
				skillName = fm.Name
			}
		}
		break
	}

	if !SkillNameRe.MatchString(skillName) {
		return "", fmt.Errorf("skill name %q is not valid", skillName)
	}

	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		return "", err
	}
	skillDir := filepath.Join(registryDir, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}

	for _, f := range files {
		rawBody, status, err := ref.RawFile(f.RelPath, token)
		if err != nil {
			return "", fmt.Errorf("download %s: %w", f.Name, err)
		}
		if status != 200 {
			return "", fmt.Errorf("download %s: HTTP %d", f.Name, status)
		}
		if int64(len(rawBody)) > MaxFileSize {
			return "", fmt.Errorf("file %s exceeds per-file size limit", f.Name)
		}
		dest := filepath.Join(skillDir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, rawBody, 0o644); err != nil {
			return "", err
		}
	}
	return skillName, nil
}
