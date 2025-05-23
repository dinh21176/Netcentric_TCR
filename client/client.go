package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:9000")
	if err != nil {
		fmt.Println("Error connecting:", err)
		return
	}
	defer conn.Close()

	fmt.Println("Connected to server.")
	fmt.Println("Type commands like:")
	fmt.Println("- deploy knight left")
	fmt.Println("- deploy fireball right")
	fmt.Println("- status")
	fmt.Println("- quit")

	// Read from server
	go func() {
		serverReader := bufio.NewReader(conn)
		for {
			response, err := serverReader.ReadString('\n')
			if err != nil {
				fmt.Println("Disconnected from server.")
				os.Exit(0)
			}
			fmt.Print(">> " + response)
		}
	}()

	// Send user input to server
	userReader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("You: ")
		text, _ := userReader.ReadString('\n')
		conn.Write([]byte(text))
	}
}
