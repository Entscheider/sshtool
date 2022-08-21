package main

import (
	"log"
	"os"
)

const sshgenhelp = "Generate SSH key"

// Generates a private/public ed25519 keypair
func main_sshgen(args []string) {
	if len(args) != 2 {
		ErrPrintf("Wrong arguments: %s outputkey\n", args[0])
		return
	}
	output := args[1]
	log.Println("Generating ...")
	priv_key, pub_key, err := GenerateServerKey()
	if err != nil {
		ErrPrintf("Error occured: %v\n", err)
		return
	}
	log.Printf("Sucessfull. Write result to \"%s\"\n", output)
	err = os.WriteFile(output, priv_key, 0600)
	if err != nil {
		log.Fatalln(err)
	}
	err = os.WriteFile(output+".pub", pub_key, 0644)
	if err != nil {
		log.Fatalln(err)
	}
}
