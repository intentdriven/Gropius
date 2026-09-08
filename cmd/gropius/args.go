package main

// refuseUnknownArgs returns the message to print, and an empty string when the
// command line is one this build understands.
//
// gropius takes flags and no subcommands. The bundle is launched by macOS with
// no arguments at all, so a bare invocation has to keep meaning "run the
// server"; everything else here is about the case where somebody, or something,
// passed a word this build has no meaning for.
//
// Only the first offending argument is named. A command line like
// `gropius doctor -root /tmp` leaves three entries in flag.Args() because flag
// stops parsing at "doctor", and reporting all three would suggest the flags
// were the problem when the first word is what went wrong.
func refuseUnknownArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return "gropius: unknown argument " + quote(args[0]) + "\n" +
		"This build takes flags and no subcommands. Run gropius with no arguments\n" +
		"to start the server, or gropius -version to see which build this is."
}

// quote wraps a value for display without pulling in a formatter, and keeps the
// output readable when the argument is empty or carries spaces.
func quote(s string) string {
	return "\"" + s + "\""
}
