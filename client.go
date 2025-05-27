package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

func main() {
	fmt.Print("Enter username: ")
	username := readLine()
	fmt.Print("Enter password: ")
	password := readLine()

	conn, err := net.Dial("tcp", "localhost:8080")
	if err != nil {
		fmt.Println("Connect error:", err)
		return
	}
	defer conn.Close()

	// Send auth
	fmt.Fprintf(conn, "%s:%s\n", username, password)

	// Launch goroutine to read from server
	go func() {
		serverReader := bufio.NewReader(conn)
		for {
			line, err := serverReader.ReadString('\n')
			if err != nil {
				fmt.Println("Disconnected from server.")
				os.Exit(0)
			}
			line = strings.TrimRight(line, "\n\r")
			fmt.Println(line)
		}
	}()

	// Main loop to read user input and send to server
	for {
		text := readLine()
		if text == "" {
			continue
		}
		fmt.Fprintf(conn, "%s\n", text)
	}
}

func readLine() string {
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
