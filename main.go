package main

import (
	bl "./basiclogger"
)

func init() {
	readConfig()
}

func main() {

	bl.InitMessageQueue(1)
	//Initializing filters
	if filters, ok := cfg["filter"]; ok {
		filters := filters.([]map[string]interface{})
		for _, filter := range filters {
			bl.AddFilter(filter)
		}
	}
	//Initializing outputs
	if outputs, ok := cfg["output"]; ok {
		outputs := outputs.([]map[string]interface{})
		for _, output := range outputs {
			bl.AddOutput(bl.InitOutput(output))
		}
	}
	//Initializing inputs
	if inputs, ok := cfg["input"]; ok {
		inputs := inputs.([]map[string]interface{})
		for _, input := range inputs {
			bl.AddInput(bl.InitInput(input))
		}
	}
	var hostname string
	if v, ok := cfg["main"]; ok {
		if v, ok := v.(map[string]interface{})["hostname"]; ok {
			hostname = v.(string)
		}
	}
	q := bl.StartMessageQueue(hostname)
	<-q
}
