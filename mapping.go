package main

import (
	_ "./input/in_logear_forwarder"
	_ "./logear/input/filetail"
	_ "./output/fluentd_forwarder"
	_ "./output/out_logear_forwarder"
)
