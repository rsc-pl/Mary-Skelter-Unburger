package switchrelease

// Production Switch graphics always use the published CPython/Pillow/NumPy
// runtime. Its absence or SHA drift must fail closed before a renderer starts.
import (
	"fmt"
	"io"

	"maryskelter-unburger/internal/graphicsruntime"
	"os"
)

func runGraphicsPython(root, _ string, args []string, stdout, stderr io.Writer) error {
	mode := os.Getenv("MS_BUNDLED_GRAPHICS_PYTHON")
	if mode == "0" {
		return fmt.Errorf("system Python graphics are not supported; public runtime required")
	}
	if mode != "" && mode != "1" {
		return fmt.Errorf("MS_BUNDLED_GRAPHICS_PYTHON must be 0 or 1")
	}
	if len(args) == 0 {
		return fmt.Errorf("missing Switch graphics script")
	}
	py, err := graphicsruntime.Resolve(root)
	if err != nil {
		return err
	}
	if err := py.Smoke(); err != nil {
		return err
	}
	cmd, err := py.GraphicsCommand(root, args[0], args[1:]...)
	if err != nil {
		return err
	}
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Switch graphics renderer: %w", err)
	}
	return nil
}
