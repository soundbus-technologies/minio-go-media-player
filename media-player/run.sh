#!/usr/bin/env bash

export ACCESS_KEY="TYPX7U63CIYTC9YZVCY6" && \
export SECRET_KEY="RSejd5pun4mHy85ZsEo53NLvEUDiCPCdQfwhCsrZ" && \
cd ../bin && ./minio-go-media-player -b sonics-multi-channel -e http://192.168.5.237:19000
