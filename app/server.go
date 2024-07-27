package main

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	// Uncomment this block to pass the first stage
	"net"
	"os"
)

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Println("Logs from your program will appear here!")

	os.Exit(listen())
}

func listen() (exit_code int) {
	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Println("Failed to bind to port 4221")
		return 1
	}

	var conn net.Conn
	for {
		if conn != nil {
			conn.Close()
			conn = nil
		}

		conn, err = l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			return 1
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	// start listening
	message := readFromConnection(conn)
	req, err := parseHTTPRequest(message)

	if err != nil {
		fmt.Println("There was an error prasing the HTTPRequest. Discarding.")
		return
	}

	if req.Method == "GET" && req.Target == "/" {
		writeToConnection(conn, []byte("HTTP/1.1 200 OK\r\n\r\n"))
		return
	} else if req.Method == "GET" && strings.SplitN(req.Target, "/", 3)[1] == "echo" {
		body := strings.SplitN(req.Target, "/", 3)[2]
		response := bodyResponse(200, body)
		writeToConnection(conn, []byte(response))
		return
	} else if req.Method == "GET" && req.Target == "/user-agent" {
		response := bodyResponse(200, req.UserAgent)
		writeToConnection(conn, []byte(response))
		return
	}
	// Default response
	writeToConnection(conn, []byte("HTTP/1.1 404 Not Found\r\n\r\n"))
	conn.Close()
	return
}

// Helper function to write to the connection
func writeToConnection(conn net.Conn, output []byte) bool {
	if _, err := conn.Write(output); err != nil {
		fmt.Println("Error writing to connection:", err.Error())
		return false
	}
	return true
}

func readFromConnection(conn net.Conn) (fullMessage []byte) {
	buffer := make([]byte, 1024)

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Println("Error reading from connection:", err.Error())
			break
		}

		fullMessage = append(fullMessage, buffer[:n]...)
		if n < len(buffer) {
			break
		}
	}

	return fullMessage
}

func parseHTTPRequest(message []byte) (req HTTPRequest, err error) {
	reader := bufio.NewReader(bytes.NewReader(message))
	// Line
	line, err := readLine(reader)
	if err != nil {
		fmt.Println("Error prasing request:", err.Error())
		return
	}
	req.Line = line
	parts := strings.SplitN(line, " ", 3)

	if len(parts) < 3 {
		fmt.Println("Error parsing line. Missing part")
		return req, fmt.Errorf("missing part when parsing line")
	}

	req.Method = parts[0]
	req.Target = parts[1]
	req.HTTPVersion = parts[2]

	// Headers
	for {
		line, err = readLine(reader)
		if err != nil {
			fmt.Println("Error parsing header part:", err.Error())
			return
		}
		if line == "" {
			break // Finished parsing header
		}

		parts := strings.SplitN(line, ": ", 2)

		switch strings.ToLower(parts[0]) {
		case "host":
			req.Host = parts[1]
		case "user-agent":
			req.UserAgent = parts[1]
		case "accept":
			req.Accept = parts[1]
		default:
			fmt.Println("Error parsing header part. Unknown label:", parts[0])
		}
	}

	// Body if it exists
	body, err := readLine(reader)
	if err == nil {
		req.Body = body
	} else if err.Error() != "EOF" {
		fmt.Println("Error trying to parse body:", err.Error())
		return
	}

	return req, nil
}

func readLine(reader *bufio.Reader) (lineStr string, err error) {
	var line []byte
	for {
		part, isPrefix, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		line = append(line, part...)
		if !isPrefix {
			return string(line), nil
		}
	}
}

func bodyResponse(responseCode int, body string) (fullResponse string) {
	switch responseCode {
	case 200:
		fullResponse = "HTTP/1.1 200 OK"
	default:
		fullResponse = "HTTP/1.1 404 Not Found"
	}
	fullResponse += "\r\n"
	fullResponse += "Content-Type: text/plain\r\n"
	fullResponse += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	fullResponse += "\r\n"
	fullResponse += body

	return fullResponse
}

type HTTPRequest struct {
	Line        string
	Body        string
	Method      string
	Target      string
	HTTPVersion string
	Host        string // Server host and port
	UserAgent   string // Client user agent
	Accept      string // Media types the client accepts
}
