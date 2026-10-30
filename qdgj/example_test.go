package qdgj_test

import (
	"context"
	"fmt"
	"log"

	"easycheck/qdgj"
)

// 以下示例展示客户端的登录和任务查询用法。
func ExampleClient_Login() {
	client, err := qdgj.NewClient(qdgj.ConfigFromEnv())
	if err != nil {
		log.Fatal(err)
	}
	if _, err := client.Login(context.Background()); err != nil {
		log.Fatal(err)
	}
	if err := client.SaveSession(); err != nil {
		log.Fatal(err)
	}
}

func ExampleClient_ListTasks() {
	client, err := qdgj.NewClient(qdgj.ConfigFromEnv())
	if err != nil {
		log.Fatal(err)
	}
	if err := client.LoadSession(); err != nil {
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
