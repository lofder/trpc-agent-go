package platform

import (
	"context"
	"fmt"
	"math"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// calculatorArgs is the input schema of the calculator tool.
type calculatorArgs struct {
	Operation string  `json:"operation" jsonschema:"description=运算类型: add|subtract|multiply|divide|power,enum=add,enum=subtract,enum=multiply,enum=divide,enum=power"`
	A         float64 `json:"a" jsonschema:"description=第一个操作数"`
	B         float64 `json:"b" jsonschema:"description=第二个操作数"`
}

// calculatorResult is the output of the calculator tool.
type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func calculate(_ context.Context, in calculatorArgs) (calculatorResult, error) {
	var r float64
	switch in.Operation {
	case "add", "+":
		r = in.A + in.B
	case "subtract", "-":
		r = in.A - in.B
	case "multiply", "*":
		r = in.A * in.B
	case "divide", "/":
		if in.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		r = in.A / in.B
	case "power", "^":
		r = math.Pow(in.A, in.B)
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation %q", in.Operation)
	}
	return calculatorResult{Operation: in.Operation, A: in.A, B: in.B, Result: r}, nil
}

// timeArgs is the input schema of the current_time tool.
type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=IANA 时区名,例如 Asia/Shanghai、UTC"`
}

// timeResult is the output of the current_time tool.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Weekday  string `json:"weekday"`
}

func currentTime(_ context.Context, in timeArgs) (timeResult, error) {
	tz := in.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
		tz = "UTC"
	}
	now := time.Now().In(loc)
	return timeResult{
		Timezone: tz,
		Time:     now.Format("2006-01-02 15:04:05"),
		Weekday:  now.Weekday().String(),
	}, nil
}

// weatherArgs is the input schema of the get_weather tool.
type weatherArgs struct {
	City string `json:"city" jsonschema:"description=城市名称,例如 深圳"`
}

// weatherResult is the output of the get_weather tool.
type weatherResult struct {
	City        string  `json:"city"`
	Condition   string  `json:"condition"`
	Temperature float64 `json:"temperature_c"`
	Humidity    int     `json:"humidity_pct"`
	Note        string  `json:"note"`
}

func getWeather(_ context.Context, in weatherArgs) (weatherResult, error) {
	city := in.City
	if city == "" {
		city = "深圳"
	}
	// Deterministic pseudo weather so test assertions are stable.
	sum := 0
	for _, r := range city {
		sum += int(r)
	}
	conds := []string{"晴", "多云", "小雨", "阴", "雷阵雨"}
	return weatherResult{
		City:        city,
		Condition:   conds[sum%len(conds)],
		Temperature: float64(18 + sum%15),
		Humidity:    40 + sum%50,
		Note:        "模拟数据（离线演示用）",
	}, nil
}

// BuildDemoTools constructs the demo toolset for the agent under test.
func BuildDemoTools() []tool.Tool {
	calc := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("执行基础数学运算 (add/subtract/multiply/divide/power)"),
	)
	tm := function.NewFunctionTool(
		currentTime,
		function.WithName("current_time"),
		function.WithDescription("获取指定时区的当前时间与日期"),
	)
	weather := function.NewFunctionTool(
		getWeather,
		function.WithName("get_weather"),
		function.WithDescription("查询指定城市的当前天气（演示用模拟数据）"),
	)
	return []tool.Tool{calc, tm, weather}
}
