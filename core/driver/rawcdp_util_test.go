package driver

import "io"

func ioPipe() (*io.PipeReader, *io.PipeWriter) { return io.Pipe() }

type closeBoth struct{ a, b io.Closer }

func (c closeBoth) Close() error { c.a.Close(); return c.b.Close() }
