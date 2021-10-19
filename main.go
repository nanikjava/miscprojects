package main

import (
	"context"
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
	"io"
	"io/ioutil"
	"log"
	"time"
)

func CreateNewContainer(cli *client.Client, image string) (string, error) {
	containerName := "chromium"
	ctx := context.Background()

	hostBinding := nat.PortBinding{
		HostIP:   "0.0.0.0",
		HostPort: "9222",
	}
	containerPort, err := nat.NewPort("tcp", "9222")
	if err != nil {
		panic("Unable to get the port")
	}

	portBinding := nat.PortMap{containerPort: []nat.PortBinding{hostBinding}}
	cont, err := cli.ContainerCreate(
		context.Background(),
		&container.Config{
			Image: image,
		},
		&container.HostConfig{
			PortBindings: portBinding,
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

	//get logs
	reader, err := cli.ContainerLogs(ctx, containerName, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     true,
		Details:    true,
	})


	if err != nil {
		panic(err)
	}

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := reader.Read(buf)
			fmt.Println(string(buf[:n]))
			if err == io.EOF {
				break
			}
		}
		fmt.Println(reader)
	}()


	return cont.ID, nil
}

func main() {
	cli, err := client.NewClientWithOpts(client.FromEnv)
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

	time.Sleep(2 * time.Second)
	// Use the DevTools json API to get the current page.
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
	defer conn.Close() // Must be closed when we are done.

	// Create a new CDP Client that uses conn.
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

	screenshotName := "screenshot.jpg"
	screenshotArgs := page.NewCaptureScreenshotArgs().
		SetFormat("jpeg").
		SetQuality(80)
	screenshot, err := c.Page.CaptureScreenshot(ctx, screenshotArgs)
	if err != nil {
		log.Fatal(err)
	}
	if err = ioutil.WriteFile(screenshotName, screenshot.Data, 0644); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Saved screenshot: %s\n", screenshotName)

	t := time.Duration(1 * time.Second)
	err = cli.ContainerStop(ctx, ctrid, &t)
	if err != nil {
		log.Fatal(err)
	}

	err = cli.ContainerRemove(ctx, ctrid, types.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Fatal(err)
	}
}
