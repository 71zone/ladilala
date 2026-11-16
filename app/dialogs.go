package main

// pickOpenPath opens the native open-file dialog. A cancelled dialog gives an
// empty path and no error. It needs the running app.
func pickOpenPath(b *Backend, filterName, pattern string) (string, error) {
	if b.App == nil {
		return "", apiErr("internal", "the window is not ready")
	}
	p, err := b.App.Dialog.OpenFile().CanChooseFiles(true).AddFilter(filterName, pattern).PromptForSingleSelection()
	if err != nil {
		return "", apiErr("internal", err.Error())
	}
	return p, nil
}

// pickSavePath opens the native save-file dialog. A cancelled dialog gives an
// empty path and no error.
func pickSavePath(b *Backend, fileName, filterName, pattern string) (string, error) {
	if b.App == nil {
		return "", apiErr("internal", "the window is not ready")
	}
	p, err := b.App.Dialog.SaveFile().SetFilename(fileName).AddFilter(filterName, pattern).PromptForSingleSelection()
	if err != nil {
		return "", apiErr("internal", err.Error())
	}
	return p, nil
}
