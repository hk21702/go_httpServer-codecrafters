package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"

	// Uncomment this block to pass the first stage
	"net"
	"os"
)

func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Println("Logs from your program will appear here!")
	directory := flag.String("directory", "", "Directory to serve files from")
	flag.Parse()

	fmt.Println("Serving files from directory:", *directory)

	os.Exit(listen(*directory))
}

func listen(dir string) (exit_code int) {
	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Println("Failed to bind to port 4221")
		return 1
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			return 1
		}

		go handleConnection(conn, dir)
	}
}

func handleConnection(conn net.Conn, directory string) {
	defer conn.Close()
	// start listening
	message := readFromConnection(conn)
	req, err := parseHTTPRequest(message)

	if err != nil {
		fmt.Println("There was an error prasing the HTTPRequest. Discarding.")
		return
	}

	var response []byte

	switch req.Method {
	case "GET":
		{
			if req.Target == "/" {
				response = []byte("HTTP/1.1 200 OK\r\n\r\n")
				break
			}
			targetParts := strings.SplitN(req.Target, "/", 3)

			switch targetParts[1] {
			case "echo":
				response = bodyResponse(200, []byte(targetParts[2]))
			case "user-agent":
				response = bodyResponse(200, []byte(req.UserAgent))
			case "files":
				{
					file := strings.SplitN(req.Target, "/", 3)[2]
					_, err := os.Stat(directory + file)

					if errors.Is(err, fs.ErrNotExist) {
						response = []byte("HTTP/1.1 404 Not Found\r\n\r\n")
					} else {
						file, err := os.ReadFile(directory + file)
						if err != nil {
							fmt.Println("Error reading file:", err.Error())
							return
						}

						response = fileResponse(200, file)
					}

				}
			default:
				response = []byte("HTTP/1.1 404 Not Found\r\n\r\n")
			}
		}
	case "POST":
		{
			targetParts := strings.SplitN(req.Target, "/", 3)
			if len(targetParts) == 1 && targetParts[0] == "/" {
				response = []byte("HTTP/1.1 200 OK\r\n\r\n")
				break
			}

			switch targetParts[1] {
			case "files":
				{
					path := directory + targetParts[2]
					err = os.WriteFile(path, []byte(req.Body), 0755)
					if err != nil {
						fmt.Println("Error writing file:", path)
						return
					}

					response = []byte("HTTP/1.1 201 Created\r\n\r\n")
				}
			}
		}
	default:
		{
			fmt.Println("Invalid request type:", req.Method)
			response = []byte("HTTP/1.1 404 Not Found\r\n\r\n")
		}
	}

	writeToConnection(conn, response)

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
		case "content-type":
			req.ContentType = parts[1]
		case "content-length":
			{
				num, err := strconv.Atoi(parts[1])
				if err != nil {
					fmt.Println("Error parsing content-length", parts[1])
					req.ContentLength = -1
				} else {
					req.ContentLength = num
				}
			}
		default:
			fmt.Println("Error parsing header part. Unknown label:", parts[0])
		}
	}

	if req.ContentLength != -1 {
		buff := make([]byte, req.ContentLength)
		_, err = io.ReadFull(reader, buff)
		if err != nil {
			fmt.Println("Error filling buffer from body")
			return
		}
		req.Body = buff

	} else {
		// Body if it exists
		var body string
		body, err = readLine(reader)
		if err == nil {
			req.Body = []byte(body)
		} else if err.Error() != "EOF" {
			fmt.Println("Error trying to parse body:", err.Error())
			return
		}
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

func bodyResponse(responseCode int, body []byte) []byte {
	var fullResponse string

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

	return append([]byte(fullResponse), body...)
}

func fileResponse(responseCode int, file []byte) (byteResponse []byte) {
	var strResponse string
	switch responseCode {
	case 200:
		strResponse = "HTTP/1.1 200 OK"
	default:
		strResponse = "HTTP/1.1 404 Not Found"
	}
	strResponse += "\r\n"
	strResponse += "Content-Type: application/octet-stream\r\n"
	strResponse += fmt.Sprintf("Content-Length: %d\r\n", len(file))
	strResponse += "\r\n"
	byteResponse = append(byteResponse, []byte(strResponse)...)
	byteResponse = append(byteResponse, file...)

	return byteResponse
}

type HTTPRequest struct {
	Line          string
	Body          []byte
	Method        string
	Target        string
	HTTPVersion   string
	Host          string // Server host and port
	UserAgent     string // Client user agent
	Accept        string // Media types the client accepts
	ContentType   string
	ContentLength int
}
