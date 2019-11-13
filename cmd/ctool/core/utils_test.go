package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/golang-collections/lib.go/assert"

	"github.com/PlatONnetwork/PlatON-Go/rlp"
)

var (
	abiFilePath = "../test/contracta.cpp.abi.json"
	configPath  = "../config.json"
)

func TestParseConfig(t *testing.T) {
	err := parseConfigJson(configPath)
	assert.Nil(t, err, fmt.Sprintf("%v", err))
	assert.NotEmpty(t, config.Gas, "the Gas is empty")
	assert.NotEmpty(t, config.GasPrice, "the GasPrice is empty")
	assert.NotEmpty(t, config.From, "the From is empty")
}

func TestParseFuncFromAbi(t *testing.T) {
	funcDesc, err := parseFuncFromAbi(abiFilePath, "atransfer")
	assert.Nil(t, err, fmt.Sprintf("%v", err))
	assert.NotNil(t, funcDesc, "the funcDesc is nil in abi")
}

func TestParseAbiFromJson(t *testing.T) {
	a, e := parseAbiFromJson(abiFilePath)
	assert.Nil(t, e, fmt.Sprintf("parse abi json error! \n，%v", e))
	marshal, e := json.Marshal(a)
	assert.Nil(t, e, fmt.Sprintf("parse data to json error! \n，%v", e))
	assert.NotNil(t, marshal, "the data is nil")
}

func TestHttpPostTransfer(t *testing.T) {

	param := JsonParam{
		Jsonrpc: "2.0",
		Method:  "platon_sendTransaction",
		//Params:[]TxParams{},
		Id: 1,
	}
	s, e := HttpPost(param)
	if e != nil {
		t.Fatal("test http post error .\n" + e.Error())
	}
	fmt.Println(s)

}

func TestHttpPostDeploy(t *testing.T) {
	deployParams := DeployParams{
		From:     "0xfb8c2fa47e84fbde43c97a0859557a36a5fb285b",
		Gas:      "0x400000",
		GasPrice: "0x9184e72a000",
	}

	params := make([]interface{}, 1)
	params[0] = deployParams
	param := JsonParam{
		Jsonrpc: "2.0",
		Method:  "platon_sendTransaction",
		Params:  params,
		Id:      1,
	}

	r, e := HttpPost(param)
	if e != nil {
		t.Fatal("test http post error .\n" + e.Error())
	}

	var resp = Response{}
	err := json.Unmarshal([]byte(r), &resp)
	if err != nil {
		t.Fatalf("parse result error ! \n %s", err.Error())
	}

	if resp.Error.Code != 0 {
		t.Fatalf("send transaction error ,error:%v", resp.Error.Message)
	}
	fmt.Printf("trasaction hash: %s\n", resp.Result)

	// Get transaction receipt according to result
	ch := make(chan string, 1)
	exit := make(chan string, 1)
	go GetTransactionReceipt(resp.Result, ch, exit)

	// Then, we use the timeout channel
	select {
	case address := <-ch:
		fmt.Printf("contract address:%s\n", address)
	case <-time.After(time.Second * 100):
		exit <- "exit"
		fmt.Printf("get contract receipt timeout...more than 100 second.\n")
	}

}

func TestHttpCallContact(t *testing.T) {

	param1 := uint(33)
	b := new(bytes.Buffer)
	rlp.Encode(b, param1)

	params := TxParams{
		From:     "0xfb8c2fa47e84fbde43c97a0859557a36a5fb285b",
		To:       "0xace6bdba54c8c359e70f541bfc1cabaf0244b916",
		Value:    "0x2710",
		Gas:      "0x76c00",
		GasPrice: "0x9184e72a000",
		//Data:"0x60fe47b10000000000000000000000000000000000000000000000000000000000000011",
	}

	param := JsonParam{
		Jsonrpc: "2.0",
		Method:  "platon_sendTransaction",
		Params:  []TxParams{params},
		Id:      1,
	}
	paramJson, _ := json.Marshal(param)
	fmt.Println(string(paramJson))
	s, e := HttpPost(param)
	if e != nil {
		t.Fatal("test http post error .\n" + e.Error())
	}
	fmt.Println(s)

}

func TestGetFuncParam(t *testing.T) {
	f := "set()"
	s, strings := GetFuncNameAndParams(f)
	fmt.Println(s)
	fmt.Println(len(strings))
}
