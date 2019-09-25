package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/vcs"
)

type IncorrectUsageError struct {
	error
}

var pkgVerCmdString = strings.TrimSpace(`
set -o pipefail
git describe --long --tags 2>/dev/null | sed 's/\([^-]*-g\)/r\1/;s/-/./g' ||
printf "r%s.%s" "$(git rev-list --count HEAD)" "$(git rev-parse --short HEAD)"
`)

var tmpl = template.Must(template.New("PKGBUILD").Parse(`
{{- /*
Variables:
- .PkgName: Required.
- .Dir:     Required. The directory name which is the destination of "git clone".
- .PkgVer:  Required.
- .Repo:    Required. Repository URL.
- .Root:    Required. The import path corresponding to the root of the repository.
- .Depends: Optional. The dependencies of this package.
- .Path:    Optional. The relative import path from the root of the repository.
- .BinName: Required. The final binary name.
*/ -}}
pkgname={{.PkgName}}
_pkgname={{.Dir}}
pkgver={{.PkgVer}}
pkgrel=1
arch=('i686' 'x86_64')
url='{{.Repo}}'
source=('git+git://{{.Root}}')
depends=({{range $i, $v := .Depends}}{{if $i}} {{end}}'{{.}}'{{end}})
makedepends=('go')
sha1sums=('SKIP')

pkgver() {
  cd "$srcdir/$_pkgname"
  ( set -o pipefail
    git describe --long --tags 2>/dev/null | sed 's/\([^-]*-g\)/r\1/;s/-/./g' ||
    printf "r%s.%s" "$(git rev-list --count HEAD)" "$(git rev-parse --short HEAD)"
  )
}

build(){
  cd "$srcdir/$_pkgname{{if .Path}}/{{.Path}}{{end}}"
  GO111MODULE=on go build -o "$srcdir/bin/{{.BinName}}"
}

package() {
  cd "$srcdir/bin"
  install -Dm755 '{{.BinName}}' "$pkgdir/usr/bin/{{.BinName}}"
}
`))

var usage = strings.TrimSpace(`
Usage: genpkgbuild-go <import-path> [-o <output>]

Specify Go import path as the argument.

e.g. genpkgbuild-go golang.org/x/tools/godoc

The output filename can be specified with -o flag. The default is PKGBUILD.
Specify "-" to write STDOUT instead of an actual file.
`)

var scn *bufio.Scanner
var w io.Writer

type TmplData struct {
	PkgName string
	Dir     string
	PkgVer  string
	Repo    string
	Root    string
	Depends []string
	Path    string
	BinName string
}

func run() error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("could not open TTY: %w", err)
	}
	defer tty.Close()
	scn = bufio.NewScanner(tty)
	w = tty

	args, outputPath, err := func() ([]string, string, error) {
		fs := flag.NewFlagSet("", flag.ExitOnError)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, usage)
			fmt.Fprintln(os.Stderr)
		}
		output := fs.String("o", "PKGBUILD", "")

		var args []string
		fs.Parse(os.Args[1:])
		for fs.NArg() > 0 {
			args = append(args, fs.Args()[0])
			fs.Parse(fs.Args()[1:])
		}
		if err := fs.Parse(os.Args[1:]); err != nil {
			return nil, "", err
		}

		return args, *output, nil
	}()
	if err != nil {
		return err
	}

	var output *os.File
	if outputPath == "-" {
		output = os.Stdout
	} else {
		output, err = os.OpenFile(outputPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
	}

	if len(os.Args) < 2 {
		return IncorrectUsageError{errors.New("specify import path")}
	}
	importPath := args[0]

	repoRoot, err := vcs.RepoRootForImportPath(importPath, true)
	if err != nil {
		return fmt.Errorf("can't get root repo for the import path: %w", err)
	}

	if repoRoot.VCS.Name != "Git" {
		return fmt.Errorf("sorry, not git repo is not supported yet: %s", repoRoot.VCS.Name)
	}

	errC := make(chan error)
	versionC := make(chan string)
	go func() {
		version, err := getVersion(repoRoot)
		errC <- err
		versionC <- version
	}()

	baseName := path.Base(repoRoot.Root)
	pkgName, err := prompt("Package Name", fmt.Sprintf("%s-git", baseName))
	if err != nil {
		return err
	}

	dependsList, err := prompt("Dependent Packages(split by space)", "")
	if err != nil {
		return err
	}
	depends := strings.Fields(dependsList)

	relPath, err := filepath.Rel(repoRoot.Root, importPath)
	if err != nil {
		return err
	}
	if relPath == "." {
		relPath = ""
	}

	binName, err := prompt("Binary name to be installed", path.Base(importPath))
	if err != nil {
		return err
	}

	fmt.Fprint(w, "Please wait...")

	if err := <-errC; err != nil {
		return err
	}
	version := <-versionC

	if output == os.Stdout {
		fmt.Fprintln(w, "===========================")
	}
	fmt.Fprintln(w)

	tmpl.Execute(output, TmplData{
		PkgName: pkgName,
		Dir:     baseName,
		PkgVer:  version,
		Repo:    repoRoot.Repo,
		Root:    repoRoot.Root,
		Depends: depends,
		Path:    relPath,
		BinName: binName,
	})
	return nil
}

func prompt(p, dflt string) (string, error) {
	if dflt != "" {
		fmt.Fprintf(w, "%s: (%s) ", p, dflt)
	} else {
		fmt.Fprintf(w, "%s: ", p)
	}
	if !scn.Scan() {
		if err := scn.Err(); err != nil {
			return "", fmt.Errorf("input error: %w", err)
		}
		return "", errors.New("interrupted")
	}
	v := strings.TrimSpace(scn.Text())
	if v == "" {
		v = dflt
	}
	return v, nil
}

func getVersion(repoRoot *vcs.RepoRoot) (string, error) {
	dir, err := ioutil.TempDir("", "genpkgbuild")
	if err != nil {
		return "", fmt.Errorf("could not secure a temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := repoRoot.VCS.Create(dir, repoRoot.Repo); err != nil {
		return "", fmt.Errorf("could not clone the repo: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), "bash", "-c", pkgVerCmdString)
	cmd.Dir = dir
	version, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Stderr.Write(exitErr.Stderr)
		}
		return "", err
	}

	return strings.TrimSpace(string(version)), nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.As(err, new(IncorrectUsageError)) {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, usage)
			fmt.Fprintln(os.Stderr)
		}
		os.Exit(1)
	}
}
