package main

import (
	"log"

	"github.com/lululu811/wechat-chatlog/cmd/chatlog"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	chatlog.Execute()
}
