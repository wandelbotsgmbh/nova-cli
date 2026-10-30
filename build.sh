OUT=bin

# the app templates are embedded with go:embed all:..., so local build artifacts
# (e.g. from running pnpm install in a template) would end up in the binary
junk=$(find pkg/generator pkg/bootstrap/templates \( -name node_modules -o -name .next -o -name '*.tsbuildinfo' -o -name .venv -o -name __pycache__ \) -prune -print)
if test -n "$junk"; then
    echo "Build artifacts found in embedded templates, remove them first:"
    echo "$junk"
    exit 1
fi
if test -d "$OUT"; then
    echo ""
else
    mkdir $OUT
fi

build() {
    export GOOS=$1
    export GOARCH=$2
    echo Build $GOOS/$GOARCH

    outFile=$OUT/nova
    if test $1 = windows; then
        outFile=$outFile.exe
    fi

    go build -ldflags="-X 'github.com/wandelbotsgmbh/nova-cli/pkg/version.cliVersion=dev'" -o $outFile
}

#build linux amd64
# other platforms
# build windows amd64 
# build darwin amd64
build darwin arm64
