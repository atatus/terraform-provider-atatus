package main

import (
	"context"
	"flag"
	"log"

	"github.com/atatus/terraform-provider-atatus/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/atatus/atatus",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), provider.New("1.0.0"), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
