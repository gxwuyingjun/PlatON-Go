package vm

import (
	"Platon-go/common"
	"Platon-go/life/utils"
	"Platon-go/log"
	"Platon-go/rlp"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"strings"

	"Platon-go/life/exec"
	"Platon-go/life/resolver"
)

// WASM解释器，用于负责解析WASM指令集，具体执行将委托至Life虚拟机完成
// 实现Interpreter的接口 run/canRun.
// WASMInterpreter represents an WASM interpreter
type WASMInterpreter struct {
	evm         *EVM
	cfg         Config
	vmContext   *exec.VMContext
	lvm         *exec.VirtualMachine
	wasmStateDB *WasmStateDB

	resolver   exec.ImportResolver
	returnData []byte
}

// NewWASMInterpreter returns a new instance of the Interpreter
func NewWASMInterpreter(evm *EVM, cfg Config) *WASMInterpreter {

	wasmStateDB := &WasmStateDB{
		StateDB: evm.StateDB,
		evm:     evm,
		cfg:     &cfg,
	}

	wasmLog := cfg.WasmLogger
	if wasmLog == nil {
		wasmLog =log.New("wasm.stderr")
		wasmLog.SetHandler(log.LvlFilterHandler(log.LvlDebug, log.StreamHandler(os.Stderr, log.FormatFunc(func(r *log.Record) []byte {
			return []byte(r.Msg)
		}))))

	}
	// 初始化WASM解释器，保存WASM虚拟机需要的配置及上下文信息
	return &WASMInterpreter{
		evm: evm,
		cfg: cfg,
		vmContext: &exec.VMContext{
			Config: exec.VMConfig{
				EnableJIT:          false,
				DefaultMemoryPages: 128,
				DynamicMemoryPages: 1,
			},
			Addr:     [20]byte{},
			GasUsed:  0,
			GasLimit: evm.Context.GasLimit,
			// 验证此处是否可行
			StateDB: wasmStateDB,
			Log: wasmLog,
		},
		wasmStateDB: wasmStateDB,
		resolver:    resolver.NewResolver(0x01),
	}
}

// Run loops and evaluates the contract's code with the given input data and returns.
// the return byte-slice and an error if one occurred
//
// It's important to note that any errors returned by the interpreter should be
// considered a revert-and-consume-all-gas operations except for
// errExecutionReverted which means revert-and-keep-gas-lfet.
func (in *WASMInterpreter) Run(contract *Contract, input []byte, readOnly bool) (ret []byte, err error) {

	in.wasmStateDB.contract = contract

	in.evm.depth++
	defer func() { in.evm.depth-- }()

	if len(contract.Code) == 0 {
		return nil, nil
	}
	_, abi, code, er := parseRlpData(contract.Code)
	if er != nil {
		return nil, er
	}

	in.vmContext.Addr = contract.Address()
	in.vmContext.GasLimit = contract.Gas // 可使用的即为受限制的

	// 获取执行器对象
	in.lvm, err = exec.NewVirtualMachine(code, *in.vmContext, in.resolver, nil)
	if err != nil {
		return nil, err
	}

	// input 代表着交易的data, 需要从中解析出entryPoint.
	contract.Input = input
	var (
		funcName string
		//txType	int		// 交易类型：合约创建、交易、投票等类型
		params []int64
		returnType string 
	)
	
	if input == nil {
		funcName = "init" // init function.
	} else {
		// parse input.
		_, funcName, params, returnType, err = parseInputFromAbi(in.lvm, input, abi)
		if err != nil {
			return nil, err
		}
	}
	entryID, ok := in.lvm.GetFunctionExport(funcName)
	if !ok {
		return nil, fmt.Errorf("entryId not found.")
	}
	res, err := in.lvm.RunWithGasLimit(entryID, int(in.vmContext.GasLimit), params...)
	if err != nil {
		in.lvm.PrintStackTrace()
		return nil, err
	}
	if contract.Gas > in.vmContext.GasUsed {
		contract.Gas = contract.Gas - in.vmContext.GasUsed
	} else {
		return nil, fmt.Errorf("out of gas.")
	}

	if input == nil {
		return contract.Code, nil
	}
	// todo: 类型缺失，待继续补充
	switch returnType {
	case "void", "int8", "int", "int32", "int64" :
		hashRes := common.BytesToHash(utils.Int64ToBytes(res))
		return hashRes.Bytes(), nil
	case "string":
		returnBytes := make([]byte, 0)
		copyData := in.lvm.Memory.Memory[res:]
		for _, v := range copyData {
			if v == 0 {
				break
			}
			returnBytes = append(returnBytes, v)
		}
		// 0x0000000000000000000000000000000000000020
		// 00000000000000000000000000000000000000000d
		// 00000000000000000000000000000000000000000
		strHash := common.BytesToHash(common.Int32ToBytes(32))
		sizeHash := common.BytesToHash(common.Int64ToBytes(int64((len(returnBytes)))))
		var dataRealSize = len(returnBytes)
		if (dataRealSize % 32) != 0 {
			dataRealSize = dataRealSize + (32 - (dataRealSize % 32))
		}
		dataByt := make([]byte, dataRealSize)
		copy(dataByt[0:], returnBytes)

		finalData := make([]byte, 0)
		finalData = append(finalData, strHash.Bytes()...)
		finalData = append(finalData, sizeHash.Bytes()...)
		finalData = append(finalData, dataByt...)

		fmt.Println("CallReturn:", string(returnBytes))
		return finalData,nil
	}
	return nil, nil
}

// CanRun tells if the contract, passed as an argument, can be run
// by the current interpreter
func (in *WASMInterpreter) CanRun(code []byte) bool {
	return true
}

// parse input(payload)
func parseInputFromAbi(vm *exec.VirtualMachine, input []byte, abi []byte) (txType int, funcName string, params []int64, returnType string, err error) {
	defer func() {
		if er := recover(); er != nil {
			txType, funcName, params, returnType, err = -1, "", nil, "", er.(error)
			vm.Context.Log.Error("Parse input data fail.", "errMsg",err.Error())
		}
	}()
	if input == nil || len(input) <= 1 {
		return -1, "", nil, "", fmt.Errorf("invalid input.")
	}
	// [txType][funcName][args1][args2]
	// rlp decode
	ptr := new(interface{})
	err = rlp.Decode(bytes.NewReader(input), &ptr)
	if err != nil {
		return -1, "", nil, "", err
	}
	rlpList := reflect.ValueOf(ptr).Elem().Interface()

	if _, ok := rlpList.([]interface{}); !ok {
		return -1, "", nil, "", fmt.Errorf("invalid rlp format.")
	}

	iRlpList := rlpList.([]interface{})
	if len(iRlpList) < 2 {
		return -1, "", nil, "", fmt.Errorf("invalid input. ele must greater than 2")
	}

	wasmabi := new(utils.WasmAbi)
	err = wasmabi.FromJson(abi)
	if err != nil {
		return -1, "", nil, "", fmt.Errorf("invalid abi, encoded fail.")
	}

	params = make([]int64, 0)
	if v, ok := iRlpList[0].([]byte); ok {
		txType = int(bytes2int64(v))
	}
	if v, ok := iRlpList[1].([]byte); ok {
		funcName = string(v)
	}

	// 查找方法名对应的args
	var args []utils.Args
	for _, v := range wasmabi.Abi {
		if strings.EqualFold(funcName, v.Method) {
			args = v.Args
			returnType = v.Return
			break
		}
	}
	argsRlp := iRlpList[2:]
	if len(args) != len(argsRlp) {
		return -1, "", nil, returnType, fmt.Errorf("invalid input or invalid abi.")
	}
	// todo: abi类型解析，需要继续添加
	// uint64 uint32  uint16 uint8 int64 int32  int16 int8 float32 float64 string void
	// 此处参数是否替换为uint64
	for i, v := range args {
		bts := argsRlp[i].([]byte)
		switch v.RealTypeName {
		case "string":
			pos := resolver.MallocString(vm, string(bts))
			params = append(params, pos)
		case "int8":
			params = append(params, int64(bts[0]))
		case "int16":
			params = append(params, int64(binary.BigEndian.Uint16(bts)))
		case "int32", "int":
			params = append(params, int64(binary.BigEndian.Uint32(bts)))
		case "int64":
			params = append(params, int64(binary.BigEndian.Uint64(bts)))
		case "uint8":
			params = append(params, int64(bts[0]))
		case "uint32", "uint":
			params = append(params, int64(binary.BigEndian.Uint32(bts)))
		case "uint64":
			params = append(params, int64(binary.BigEndian.Uint64(bts)))
		case "bool":
			params = append(params, int64(bts[0]))
		}
	}

	return txType, funcName, params, returnType,nil
}

// rlpData=RLP([txType][code][abi])
func parseRlpData(rlpData []byte) (int64, []byte, []byte, error) {
	ptr := new(interface{})
	err := rlp.Decode(bytes.NewReader(rlpData), &ptr)
	if err != nil {
		return -1, nil, nil, err
	}
	rlpList := reflect.ValueOf(ptr).Elem().Interface()

	if _, ok := rlpList.([]interface{}); !ok {
		return -1, nil, nil, fmt.Errorf("invalid rlp format.")
	}

	iRlpList := rlpList.([]interface{})
	if len(iRlpList) <= 2 {
		return -1, nil, nil, fmt.Errorf("invalid input. ele must greater than 2")
	}
	var (
		txType int64
		code   []byte
		abi    []byte
	)
	if v, ok := iRlpList[0].([]byte); ok {
		txType = utils.BytesToInt64(v)
	}
	if v, ok := iRlpList[1].([]byte); ok {
		code = v
		//fmt.Println("dstCode: ", common.Bytes2Hex(code))
	}
	if v, ok := iRlpList[2].([]byte); ok {
		abi = v
		//fmt.Println("dstAbi:", common.Bytes2Hex(abi))
	}
	return txType, abi, code, nil
}
