package gobin

import (
	"context"
	"debug/buildinfo"
	"errors"
	"io"
	"strings"
	_ "unsafe" // for error linkname tricks

	"github.com/Masterminds/semver"
	"github.com/quay/zlog"

	"github.com/quay/claircore"
)

//go:linkname errNotGoExe debug/buildinfo.errNotGoExe
var errNotGoExe error

// It's frustrating that there's no good way to check the error returned from
// [buildinfo.Read]. It's either doing a string compare, which will break
// silently if the error's contents are changed, or the linker tricks done here,
// which will break loudly if the error is renamed or built differently.

func toPackages(ctx context.Context, out *[]*claircore.Package, p string, r io.ReaderAt) error {
	bi, err := buildinfo.Read(r)
	switch {
	case errors.Is(err, nil):
	case errors.Is(err, errNotGoExe):
		return nil
	default:
		zlog.Debug(ctx).
			Err(err).
			Msg("unable to open executable")
		return nil
	}
	ctx = zlog.ContextWithValues(ctx, "exe", p)
	pkgdb := "go:" + p
	badVers := make(map[string]string)
	defer func() {
		if len(badVers) == 0 {
			return
		}
		zlog.Warn(ctx).
			Interface("module_versions", badVers).
			Msg("invalid semantic versions found in binary")
	}()

	// TODO(hank) This package could use canonical versions, but the
	// [claircore.Version] type is lossy for pre-release versions (I'm sorry).
	var runtimeVer claircore.Version
	rtv, err := semver.NewVersion(strings.TrimPrefix(bi.GoVersion, "go"))
	switch {
	case errors.Is(err, nil):
		runtimeVer = fromSemver(rtv)
	case errors.Is(err, semver.ErrInvalidSemVer):
		badVers["runtime"] = bi.GoVersion
	default:
		return err
	}

	*out = append(*out, &claircore.Package{
		Kind:              claircore.BINARY,
		Name:              "runtime",
		Version:           bi.GoVersion,
		PackageDB:         pkgdb,
		Filepath:          p,
		NormalizedVersion: runtimeVer,
	})

	ev := zlog.Debug(ctx)
	vs := map[string]string{
		"runtime": bi.GoVersion,
	}
	var mainVer claircore.Version
	mpv, err := semver.NewVersion(bi.Main.Version)
	switch {
	case errors.Is(err, nil):
		mainVer = fromSemver(mpv)
	case errors.Is(err, semver.ErrInvalidSemVer):
		badVers[bi.Main.Path] = bi.Main.Version
	default:
		return err
	}

	*out = append(*out, &claircore.Package{
		Kind:              claircore.BINARY,
		PackageDB:         pkgdb,
		Name:              bi.Main.Path,
		Version:           bi.Main.Version,
		Filepath:          p,
		NormalizedVersion: mainVer,
	})

	if ev.Enabled() {
		vs[bi.Main.Path] = bi.Main.Version
	}
	for _, d := range bi.Deps {
		var nv claircore.Version
		ver, err := semver.NewVersion(d.Version)
		switch {
		case errors.Is(err, nil):
			nv = fromSemver(ver)
		case errors.Is(err, semver.ErrInvalidSemVer):
			badVers[d.Path] = d.Version
		default:
			return err
		}

		*out = append(*out, &claircore.Package{
			Kind:              claircore.BINARY,
			PackageDB:         pkgdb,
			Name:              d.Path,
			Version:           d.Version,
			Filepath:          p,
			NormalizedVersion: nv,
		})

		if ev.Enabled() {
			vs[d.Path] = d.Version
		}
	}
	ev.
		Interface("versions", vs).
		Msg("analyzed exe")
	return nil
}

// FromSemver is the SemVer to claircore.Version mapping used by this package.
func fromSemver(v *semver.Version) (out claircore.Version) {
	out.Kind = `semver`
	// Leave a leading epoch, for good measure.
	out.V[1] = int32(v.Major())
	out.V[2] = int32(v.Minor())
	out.V[3] = int32(v.Patch())
	return out
}
