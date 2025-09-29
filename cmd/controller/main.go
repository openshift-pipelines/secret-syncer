package main

import (
	"github.com/zakisk/secret-service/pkg/reconciler"

	"knative.dev/pkg/injection/sharedmain"
)

func main() {
	sharedmain.Main("syncer-service", reconciler.NewController())
}
