package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	connect "github.com/bufbuild/connect-go"
	"github.com/bwmarrin/snowflake"
	_ "github.com/lib/pq"
	v1 "github.com/lukeshay/snowflaker/gen/proto/snowflaker/v1"
	v1connect "github.com/lukeshay/snowflaker/gen/proto/snowflaker/v1/snowflakerv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	machineId, found := os.LookupEnv("FLY_MACHINE_ID")
	if !found {
		panic("FLY_MACHINE_ID not found")
	}
	databaseUrl, found := os.LookupEnv("DATABASE_URL")
	if !found {
		panic("DATABASE_URL not found")
	}

	pool, err := sql.Open("postgres", databaseUrl)
	if err != nil {
		panic(err)
	}

	defer pool.Close()

	_, err = pool.Exec(`CREATE TABLE IF NOT EXISTS nodes (
    id varchar(255) PRIMARY KEY,
    node_id int NOT NULL
  )`)
	if err != nil {
		panic(err)
	}

	var nodeId int64

	err = pool.QueryRow("SELECT node_id FROM nodes WHERE id = $1", machineId).Scan(&nodeId)
	if err != nil {
		err = pool.QueryRow("SELECT ( (COUNT(node_id)+1) * (COUNT(node_id)+2) / 2) - SUM(node_id) FROM nodes").Scan(&nodeId)
		if err != nil || nodeId < 1 {
			nodeId = 1
		}

		// insert new node
		_, err = pool.Exec("INSERT INTO nodes (id, node_id) VALUES ($1, $2)", machineId, nodeId)
		if err != nil {
			panic(err)
		}
	}

	node, err := snowflake.NewNode(nodeId)
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	path, handler := v1connect.NewSnowflakerServiceHandler(&SnowflakerServiceHandler{
		node:   node,
		nodeId: nodeId,
	})
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	fmt.Printf("Node ID: %d\n", nodeId)
	fmt.Printf("Machine ID: %s\n", machineId)
	fmt.Printf("Path: %s\n", path)
	fmt.Println("Starting snowflaker on :8080...")

	go func() {
		if err := http.ListenAndServe(
			":8080",
			// Use h2c so we can serve HTTP/2 without TLS.
			h2c.NewHandler(mux, &http2.Server{}),
		); err != nil {
			panic(err)
		}
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Println("Shutting down snowflaker...")
	pool.ExecContext(ctx, "DELETE FROM nodes WHERE id = $1", machineId)
}

type SnowflakerServiceHandler struct {
	v1connect.UnimplementedSnowflakerServiceHandler

	node   *snowflake.Node
	nodeId int64
}

func (s *SnowflakerServiceHandler) GetId(ctx context.Context, req *connect.Request[v1.GetIdRequest]) (*connect.Response[v1.GetIdResponse], error) {
	res := connect.NewResponse(&v1.GetIdResponse{
		Id:     s.node.Generate().Int64(),
		NodeId: s.nodeId,
	})

	res.Header().Set("Snowflaker-Version", "v1")

	return res, nil
}
