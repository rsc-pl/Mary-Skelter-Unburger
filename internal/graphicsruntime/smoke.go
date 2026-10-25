package graphicsruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const smoke = `import sys
sys.path.insert(0, sys.argv[1])
import PIL, numpy
from PIL import Image, ImageDraw, ImageFilter, ImageFont
want = sys.argv[2:5]
actual = ['.'.join(map(str, sys.version_info[:3])), PIL.__version__, numpy.__version__]
if actual != want: raise RuntimeError('runtime versions %s != %s' % (actual,want))
im = Image.new('RGBA', (4,4), (15,20,25,128))
ImageDraw.Draw(im).point((0,0), fill=(35,40,45,128))
im = im.filter(ImageFilter.GaussianBlur(0.5))
if numpy.asarray(im).shape != (4,4,4): raise RuntimeError('Pillow/NumPy image bridge failed')
print('GRAPHICS_RUNTIME_SMOKE_PASS')
`

// Smoke verifies imported native extensions and real interpreter identity,
// not just checksums of the installed files. Must run before private staging.
// The path, versions and full public contents must first pass Resolve().
func (py Python) Smoke() error {
	if py.Path == "" || py.SitePackages == "" || py.PythonVersion == "" || py.PillowVersion == "" || py.NumPyVersion == "" {
		return fmt.Errorf("missing verified graphics Python runtime metadata")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py.Path, "-I", "-S", "-B", "-c", smoke,
		py.SitePackages, py.PythonVersion, py.PillowVersion, py.NumPyVersion)
	cmd.Env = py.Environment(os.Environ(), "")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bundled Python/Pillow/NumPy self-test: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) != "GRAPHICS_RUNTIME_SMOKE_PASS" {
		return fmt.Errorf("unexpected bundled graphics smoke output: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
