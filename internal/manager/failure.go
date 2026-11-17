package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/hls"
	"github.com/amemiya02/nimget/internal/httporigin"
)

func classifyFailure(err error) download.Failure {
	var request *url.Error
	var response *httporigin.StatusError
	switch {
	case errors.As(err, &response) && (response.Code == http.StatusUnauthorized || response.Code == http.StatusForbidden):
		return download.FailureAuthorization
	case errors.Is(err, download.ErrDiskSpace), diskFull(err):
		return download.FailureDiskSpace
	case errors.Is(err, download.ErrStalled), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, context.DeadlineExceeded), errors.As(err, &request):
		return download.FailureNetwork
	case errors.Is(err, fs.ErrPermission):
		return download.FailureFilesystemPermission
	case errors.Is(err, hls.ErrLive), errors.Is(err, hls.ErrUnsupported), errors.Is(err, download.ErrUnsupportedMedia):
		return download.FailureUnsupportedMedia
	default:
		return ""
	}
}

// File names can be derived from credentials embedded in a source URL.
func taskDiagnostic(err error) string {
	var path *os.PathError
	var link *os.LinkError
	switch {
	case errors.As(err, &path):
		return fmt.Sprintf("%s: %v", path.Op, path.Err)
	case errors.As(err, &link):
		return fmt.Sprintf("%s: %v", link.Op, link.Err)
	default:
		return err.Error()
	}
}
