package main

import (
	"log"

	"github.com/lsongdev/bambulab-go/bambulab"
)

func main() {
	c := bambulab.NewClient()
	resp, err := c.Login(&bambulab.Credential{
		Account:  "song940@gmail.com",
		Password: "",
		// Code: "217374",
	})
	if err != nil {
		panic(err)
	}
	log.Println(resp)
}
