#!/bin/sh

#git tag --points-at HEAD
go build -ldflags="-X 'main.Version=$(git describe --tags)'"