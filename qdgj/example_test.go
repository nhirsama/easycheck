package qdgj_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"easycheck/qdgj"
)

// 这些示例只编译，不在测试时执行网络请求。
func ExampleClient_Login() {
	client, err := qdgj.NewClient(qdgj.Options{})
	if err != nil {
		log.Fatal(err)
	}
	saved, err := client.Login(context.Background(), qdgj.LoginOptions{
		Username: os.Getenv("EASYCHECK_USERNAME"), Password: os.Getenv("EASYCHECK_PASSWORD"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := saved.Save("session.json"); err != nil {
		log.Fatal(err)
	}
}

func ExampleClient_ListTasks() {
	saved, err := qdgj.LoadSession("session.json")
	if err != nil {
		log.Fatal(err)
	}
	client, err := qdgj.NewClientFromSession(saved, qdgj.Options{})
	if err != nil {
		log.Fatal(err)
	}
	result, err := client.ListTasks(context.Background(), qdgj.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, task := range result.Tasks {
		fmt.Println(task.TaskID, task.BatchID, task.Name, task.Status, task.CheckInStatus)
	}
}
