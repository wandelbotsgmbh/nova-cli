OUT=bin
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
