// Component s03 — `mime` tool. Detects a file's true MIME type by
// inspecting its magic bytes (gabriel-vasile/mimetype) and compares
// against the filename extension. For image types it also reports
// dimensions and EXIF metadata.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/stream"
	fstools "github.com/blouargant/yoke/core/tools"
)

func main() {
	ctx := context.Background()
	llm, err := agentkit.NewModel(ctx)
	must(err)

	// Prepare a deceptive file: PNG bytes saved with a .txt suffix.
	// The mime tool should report the mismatch.
	const pngMagic = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"
	path := "mystery.txt"
	must(os.WriteFile(path, []byte(pngMagic), 0o644))
	defer os.Remove(path)

	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s03_mime",
		Description: "MIME detection demo.",
		Model:       llm,
		Instruction: "Use the mime tool on the path given, then explain in one sentence " +
			"whether the file extension matches the actual content.",
		Tools: fstools.New(),
	})
	must(err)
	r, err := agentkit.Runner("s03", a)
	must(err)

	prompt := fmt.Sprintf("Inspect %q with the mime tool and tell me whether the extension is honest.", path)
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	must(stream.Print(os.Stdout, agentkit.RunOnce(ctx, r, prompt)))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
