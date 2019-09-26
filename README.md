# genpkgbuild-go

The PKGBUILD generator for applications written in Go.

![Demo Video](./demo.png)

Output PKGBUILD:

```bash
pkgname=golint-git
_pkgname=lint
pkgver=r179.414d861
pkgrel=1
arch=('i686' 'x86_64')
url='https://github.com/golang/lint'
source=('git+git://github.com/golang/lint')
depends=()
makedepends=('go')
sha1sums=('SKIP')

pkgver() {
  cd "$srcdir/$_pkgname"
  ( set -o pipefail
    git describe --long --tags 2>/dev/null | sed 's/\([^-]*-g\)/r\1/;s/-/./g' ||
    printf "r%s.%s" "$(git rev-list --count HEAD)" "$(git rev-parse --short HEAD)"
  )
}

build() {
  cd "$srcdir/$_pkgname/golint"
  GO111MODULE=on go build -o "$srcdir/bin/golint"
}

package() {
  cd "$srcdir/bin"
  install -Dm755 'golint' "$pkgdir/usr/bin/golint"
}
```

## Usage

Specify **Go import path** as the argument.

```
$ genpkgbuild-go github.com/golang/lint/golint
```

## Installation

Install from AUR.

```
$ yay genpkgbuild-go
```
