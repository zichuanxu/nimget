package quarantine

import (
	"errors"
	"os"

	"github.com/amemiya02/nimget/internal/partialfile"
)

// Mark writes the Zone.Identifier stream for the Internet zone.
func Mark(path, sourceURL string) error {
	return os.WriteFile(path+":Zone.Identifier", markBody(sourceURL), 0o644)
}

// MarkRooted marks the file while the caller's writable handle pins it in root.
func MarkRooted(root *os.Root, _ *os.File, name, sourceURL string) error {
	stream, err := partialfile.OpenRootedStream(root, name, "Zone.Identifier")
	if err != nil {
		return err
	}
	_, err = stream.Write(markBody(sourceURL))
	return errors.Join(err, stream.Sync(), stream.Close())
}

func markBody(sourceURL string) []byte {
	body := "[ZoneTransfer]\r\nZoneId=3\r\n"
	if o := origin(sourceURL); o != "" {
		body += "HostUrl=" + o + "\r\n"
	}
	return []byte(body)
}
