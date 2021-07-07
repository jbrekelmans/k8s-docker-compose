package up

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	containerService "github.com/kube-compose/kube-compose/internal/pkg/container/service"
	"github.com/kube-compose/kube-compose/internal/pkg/fs"
	"github.com/kube-compose/kube-compose/internal/pkg/util"
	"github.com/pkg/errors"
)

var tarFileInfoHeader = tar.FileInfoHeader

func buildVolumeInitImageGetDockerfile(isDirSlice []bool) []byte {
	var b bytes.Buffer
	b.WriteString(`ARG BASE_IMAGE
FROM ${BASE_IMAGE}
`)
	for i := 1; i <= len(isDirSlice); i++ {
		if isDirSlice[i-1] {
			fmt.Fprintf(&b, "COPY data%d/ /app/data/vol%d/\n", i, i)
		} else {
			fmt.Fprintf(&b, "COPY data%d /app/data/vol%d\n", i, i)
		}
	}
	b.WriteString(`ENTRYPOINT ["bash", "-c", "`)
	for i := 1; i <= len(isDirSlice); i++ {
		if i > 1 {
			b.WriteString(" && ")
		}
		fmt.Fprintf(&b, "cp -ar /app/data/vol%d /mnt/vol%d/root", i, i)
	}
	b.WriteString(`"]
`)
	return b.Bytes()
}

type TarWriter interface {
	io.Writer
	WriteHeader(header *tar.Header) error
}

type bindMountHostFileToTarHelper struct {
	tw                     TarWriter
	renameTo               string
	rootHostFile           string
	rootHostFileVol        string
	rootHostFileWithoutVol string
}

func (h *bindMountHostFileToTarHelper) runRegular(fileInfo os.FileInfo, hostFile, fileNameInTar string) error {
	header, err := tarFileInfoHeader(fileInfo, "")
	if err != nil {
		return err
	}
	fd, err := fs.OS.Open(hostFile)
	if err != nil {
		return err
	}
	defer util.CloseAndLogError(fd)
	header.Name = fileNameInTar
	err = h.endHeaderCommon(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(h.tw, fd)
	return err
}

func (h *bindMountHostFileToTarHelper) runDirectory(fileInfo os.FileInfo, hostFile, fileNameInTar string) error {
	fd, err := fs.OS.Open(hostFile)
	if err != nil {
		return err
	}
	defer util.CloseAndLogError(fd)
	header, err := tarFileInfoHeader(fileInfo, "")
	if err != nil {
		return err
	}
	header.Name = fileNameInTar + "/"
	err = h.endHeaderCommon(header)
	if err != nil {
		return err
	}
	entries, err := fd.Readdir(0)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		err = h.runRecursive(
			entry,
			hostFile+string(filepath.Separator)+entry.Name(),
			header.Name+entry.Name(),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *bindMountHostFileToTarHelper) isFileWithinBindHostRoot(target string) bool {
	// Can assume target and h.rootHostFile are cleaned.
	// TODO https://github.com/kube-compose/kube-compose/issues/173 support case sensitive file systems
	// We do not have to split off the prefix here, but we do so in case drive letters are case-insensitive
	// independent of the file system.
	vol := filepath.VolumeName(target)
	if vol != h.rootHostFileVol {
		return false
	}
	targetWithoutVol := target[len(vol):]
	if strings.HasPrefix(targetWithoutVol, h.rootHostFileWithoutVol) {
		if len(targetWithoutVol) == len(h.rootHostFileWithoutVol) {
			return true
		}
		if targetWithoutVol[len(h.rootHostFileWithoutVol)] == filepath.Separator {
			return true
		}
	}
	return false
}

func (h *bindMountHostFileToTarHelper) runSymlink(fileInfo os.FileInfo, hostFile, fileNameInTar string) error {
	// Symbolic link
	link, err := fs.OS.Readlink(hostFile)
	if err != nil {
		return errors.Wrapf(err, "error while reading link %#v", hostFile)
	}
	var linkResolved string
	linkIsAbsLike := link != "" && (link[0] == '\\' || link[0] == '/')
	if linkIsAbsLike || filepath.VolumeName(link) != "" {
		// Windows:
		// Handle situations where the link is absolute (but does not have a drive), or relative to the cwd of a drive:
		// https://docs.microsoft.com/en-us/windows/desktop/api/winbase/nf-winbase-createsymboliclinka#remarks
		// This should be a noop on non-Windows because there will never be a non-empty VolumeName and therefore the path must
		// be absolute.
		linkResolved, err = fs.OS.Abs(link)
		if err != nil {
			return errors.Wrapf(err, "error while converting %#v to an absolute path", link)
		}
	} else {
		// Windows: no drive.
		// Therefore the link is relative to the parent directory.
		linkResolved = filepath.Join(filepath.Dir(hostFile), link)
	}
	// linkResolved will always be cleaned here, which is required for isFileWithinBindHostRoot.
	if h.isFileWithinBindHostRoot(linkResolved) {
		// Convert the target to an absolute path within the tar, normalising slashes.
		linkResolvedInTar := filepath.ToSlash(h.renameTo + linkResolved[len(h.rootHostFile):])
		// Convert the target to a relative path within the tar. This can be done a bit more efficiently since we know the paths are
		// relative, cleaned and slashed. We assign the error to underscore because it should never happen.
		linkResolvedInTarRel, _ := filepath.Rel(filepath.Dir(fileNameInTar), linkResolvedInTar)
		header, err := tarFileInfoHeader(fileInfo, linkResolvedInTarRel)
		if err != nil {
			return err
		}
		header.Name = fileNameInTar
		return h.endHeaderCommon(header)
	}
	return fmt.Errorf("target of symlink %#v it outside the bind volume with host %#v", hostFile, h.rootHostFile)
}

func (h *bindMountHostFileToTarHelper) runRecursive(fileInfo os.FileInfo, hostFile, fileNameInTar string) error {
	switch {
	case (fileInfo.Mode() & os.ModeSymlink) != 0:
		// Symlink...
		return h.runSymlink(fileInfo, hostFile, fileNameInTar)
	case fileInfo.IsDir():
		// Directory...
		return h.runDirectory(fileInfo, hostFile, fileNameInTar)
	case fileInfo.Mode().IsRegular():
		// Regular file...
		return h.runRegular(fileInfo, hostFile, fileNameInTar)
	default:
		// The file is something else (e.g. a socket, a named pipe, a (char)device or an irregular file)
		return fmt.Errorf("file %#v is neither a symlink, a directory nor a regular file (os.ModeType 0x%x)",
			hostFile, fileInfo.Mode()&os.ModeType)
	}
}

func (h *bindMountHostFileToTarHelper) endHeaderCommon(header *tar.Header) error {
	// TODO https://github.com/kube-compose/kube-compose/issues/154 change owner of files here, as appropriate...
	// For example:
	// header.Uid = ...
	// header.Gid = ...
	return h.tw.WriteHeader(header)
}

func (h *bindMountHostFileToTarHelper) run(hostFile, fileNameInTar string) (isDir bool, err error) {
	fileInfo, err := fs.OS.Lstat(hostFile)
	if err != nil {
		return
	}
	isDir = fileInfo.IsDir()
	err = h.runRecursive(fileInfo, hostFile, fileNameInTar)
	return
}

func bindMountHostFileToTar(tw TarWriter, hostFile, renameTo string) (isDir bool, err error) {
	h := &bindMountHostFileToTarHelper{
		tw:           tw,
		rootHostFile: hostFile,
		renameTo:     renameTo,
	}
	vol := filepath.VolumeName(hostFile)
	h.rootHostFileVol = vol
	h.rootHostFileWithoutVol = hostFile[len(vol):]

	isDir, err = h.run(hostFile, renameTo)
	return
}

func buildVolumeInitImageGetBuildContext(bindVolumeHostPaths []string) ([]byte, error) {
	var tarBuffer bytes.Buffer
	tw := tar.NewWriter(&tarBuffer)
	defer tw.Close()

	var isDirSlice []bool
	for i, bindVolumeHostFile := range bindVolumeHostPaths {
		isDir, err := bindMountHostFileToTar(tw, bindVolumeHostFile, fmt.Sprintf("data%d", i+1))
		if err != nil {
			return nil, err
		}
		isDirSlice = append(isDirSlice, isDir)
	}

	// Write Dockerfile to build context.
	dockerFile := buildVolumeInitImageGetDockerfile(isDirSlice)
	err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Size: int64(len(dockerFile)),
	})
	if err != nil {
		return nil, err
	}
	_, err = tw.Write(dockerFile)
	if err != nil {
		return nil, err
	}
	err = tw.Flush()
	if err != nil {
		return nil, err
	}
	return tarBuffer.Bytes(), nil
}

func buildVolumeInitImage(
	ctx context.Context,
	cs containerService.ContainerService,
	bindVolumeHostPaths []string,
	volumeInitBaseImage string) (string, error) {
	buildContextBytes, err := buildVolumeInitImageGetBuildContext(bindVolumeHostPaths)
	if err != nil {
		return "", err
	}
	buildContext := bytes.NewReader(buildContextBytes)
	return cs.ImageBuild(&containerService.ImageBuildOptions{
		BuildArgs: map[string]*string{
			"BASE_IMAGE": util.NewString(volumeInitBaseImage),
		},
		BuildContext: buildContext,
		Context:      ctx,
	})
}

func resolveBindVolumeHostPath(name string) (string, error) {
	name, err := fs.OS.Abs(name)
	if err != nil {
		return "", err
	}
	// Walk sections of path, evaluating symlinks in the process.
	vol := filepath.VolumeName(name)
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.Clean(name[len(vol):]), sep)
	result := vol
	for i := 1; i < len(parts); i++ {
		result = result + sep + parts[i]
		resultResolved, err := fs.OS.EvalSymlinks(result)
		if os.IsNotExist(err) {
			if i+1 < len(parts) {
				result = result + sep + strings.Join(parts[i+1:], sep)
			}
			err = fs.OS.MkdirAll(result, os.ModePerm)
			return result, err
		}
		if err != nil {
			return "", err
		}
		result = resultResolved
	}
	return result, nil
}
