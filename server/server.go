package main

import (
	"bufio"
	"fmt"
	"net"
	"strings"
)

type Player struct {
	name  string
	hp    int
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	enemyHP := 2000
	
	send := func(msg string) {
		fmt.Fprintln(conn, msg)
	}

	send("Welcome to Text Clash Royale!")
	send("Commands: deploy knight|archer|fireball lane(left|right), status, quit")

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		input := strings.ToLower(scanner.Text())
		if input == "quit" {
			send("Goodbye!")
			return
		}
		if input == "status" {
			send(fmt.Sprintf("🏰 Enemy Tower HP: %d", enemyHP))
			continue
		}

		parts := strings.Fields(input)
		if len(parts) == 3 && parts[0] == "deploy" {
			card, lane := parts[1], parts[2]
			var dmg int
			switch card {
			case "knight":
				dmg = 150
			case "archer":
				dmg = 100
			case "fireball":
				dmg = 250
			default:
				send("Unknown card")
				continue
			}
			enemyHP -= dmg
			send(fmt.Sprintf("%s deployed to %s lane! ⚔️ Dealt %d damage.", strings.Title(card), lane, dmg))
			send(fmt.Sprintf("🏰 Enemy Tower HP: %d", enemyHP))
			if enemyHP <= 0 {
				send("🎉 You win! Tower destroyed.")
				return
			}
		} else {
			send("Invalid command.")
		}
	}
}

func main() {
	ln, err := net.Listen("tcp", ":9000")
	if err != nil {
		panic(err)
	}
	fmt.Println("Server listening on port 9000 (TCP)...")

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Connection error:", err)
			continue
		}
		go handleConnection(conn)
	}
}
