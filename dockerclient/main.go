package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/protocol/runtime"
	"github.com/mafredri/cdp/rpcc"
	"io/ioutil"
	"log"
	"time"
)

//CreateNewContainer to create new container
func CreateNewContainer(cli *client.Client, image string) (string, error) {
	containerName := "chromium"
	ctx := context.Background()

	// port 9222 for chrome
	hostBinding := nat.PortBinding{
		HostIP:   "0.0.0.0",
		HostPort: "9222",
	}
	containerPort, err := nat.NewPort("tcp", "9222")
	if err != nil {
		panic("Unable to get the port")
	}

	portBinding := nat.PortMap{containerPort: []nat.PortBinding{hostBinding}}

	//create container using the specified configurations
	cont, err := cli.ContainerCreate(
		context.Background(),
		&container.Config{
			Image: image,
		},
		&container.HostConfig{
			PortBindings: portBinding,
			//SYS_ADMIN is needed otherwise it won't work
			CapAdd:       []string{"SYS_ADMIN"},
		}, nil,
		nil,
		containerName)
	if err != nil {
		panic(err)
	}

	err = cli.ContainerStart(ctx, cont.ID, types.ContainerStartOptions{})
	if err != nil {
		panic(err)
	}

	fmt.Println("Container %s is started", cont.ID)

	go ContainerLog(cli, ctx, cont.ID, func(line string) bool {
		fmt.Print("|-| " + line)

		return true
	})

	return cont.ID, nil
}

//ContainerLog to print out container logs both stdout and stderr
func ContainerLog(cli *client.Client, ctx context.Context, containerId string,
	handler func(line string) bool) {
	i, err := cli.ContainerLogs(ctx, containerId, types.ContainerLogsOptions{
		ShowStderr: true,
		ShowStdout: true,
		Follow:     true,
	})

	if err != nil {
		log.Fatal(err)
	}

	//log header - 8 bytes
	header := make([]byte, 8)

	// for loop to read container's log
	for {
		_, err := i.Read(header)
		if err != nil {
			break
		}

		// get count of the message and create array
		count := binary.BigEndian.Uint32(header[4:])
		dat := make([]byte, count)

		_, err = i.Read(dat)

		// send the data to the handler
		handler(string(dat))
	}
	log.Printf("\nContainer %s is closed\n", containerId)
}

func main() {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	defer cli.Close()

	if err != nil {
		fmt.Println("Unable to create docker client")
		panic(err)
	}

	ctrid, err := CreateNewContainer(cli, "justinribeiro/chrome-headless:latest")
	if err != nil {
		fmt.Println("error CreateNewContainer")
		panic(err)
	}

	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)

	//sleep for 2 seconds to allow the container to spin up
	time.Sleep(2 * time.Second)

	// Use the DevTools
	devt := devtool.New("http://localhost:9222")
	p, err := devt.Get(ctx, devtool.Page)
	if err != nil {
		fmt.Println(err)
		p, err = devt.Create(ctx)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Connect to Chrome Debugging Protocol target.
	conn, err := rpcc.DialContext(ctx, p.WebSocketDebuggerURL)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// Create new CDP
	c := cdp.NewClient(conn)

	// Enable events on the Page domain.
	if err = c.Page.Enable(ctx); err != nil {
		log.Fatal(err)
	}

	// New DOMContentEventFired client will receive and buffer
	// ContentEventFired events from now on.
	domContentEventFired, err := c.Page.DOMContentEventFired(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer domContentEventFired.Close()

	// Create the Navigate arguments with the optional Referrer field set.
	navArgs := page.NewNavigateArgs("https://golang.org")
	_, err = c.Page.Navigate(ctx, navArgs)
	if err != nil {
		log.Fatal(err)
	}

	// Block until a DOM ContentEventFired event is triggered.
	if _, err = domContentEventFired.Recv(); err != nil {
		log.Fatal(err)
	}

	evalArgs := runtime.NewEvaluateArgs("document.header")
	reply, err := c.Runtime.Evaluate(ctx, evalArgs)
	if err != nil {
		log.Fatal(err)
	}

	log.Println(string(reply.Result.Value))

	//let's get screenshot of the screen
	screenshotName := "screenshot.jpg"
	screenshotArgs := page.NewCaptureScreenshotArgs().
		SetFormat("jpeg").
		SetQuality(80)

	//capture the screenshot
	screenshot, err := c.Page.CaptureScreenshot(ctx, screenshotArgs)
	if err != nil {
		log.Fatal(err)
	}

	//write the screenshot to file
	if err = ioutil.WriteFile(screenshotName, screenshot.Data, 0644); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Saved screenshot: %s\n", screenshotName)

	//completely remove the container
	err = cli.ContainerRemove(ctx, ctrid, types.ContainerRemoveOptions{
		Force: true,
	})

	if err != nil {
		log.Fatal(err)
	}
}
