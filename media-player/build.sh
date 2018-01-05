#!/bin/sh

govendor add +external
govendor fmt +local
rm -rf ../bin && go build -o ../bin/minio-go-media-player && cp -R ../web ../bin/web