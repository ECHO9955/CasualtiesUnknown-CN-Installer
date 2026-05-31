package main

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	regSubKey      = `Software\Valve\Steam`
	regValueName   = "SteamPath"
	libraryVDFName = "libraryfolders.vdf"

	gameDirName = "Casualties Unknown Demo"
	gameExeName = "CasualtiesUnknown.exe"

	patchZipName = "6.1.MOD.zip"
)

func main() {
	if runtime.GOOS != "windows" {
		fatalf("此工具只支持 Windows。")
	}

	if err := run(); err != nil {
		fatalf("%v", err)
	}

	fmt.Println("汉化安装完成。")
	waitBeforeExit()
}

func run() error {
	steamPath, err := readSteamPath()
	if err != nil {
		return err
	}
	fmt.Printf("Steam 路径: %s\n", steamPath)

	libraries, err := steamLibraries(steamPath)
	if err != nil {
		return err
	}
	fmt.Println("Steam 库路径:")
	for _, library := range libraries {
		fmt.Printf("  %s\n", library)
	}

	gameRoot, err := findGameRoot(libraries)
	if err != nil {
		return err
	}
	fmt.Printf("游戏目录: %s\n", gameRoot)

	zipPath, err := findPatchZip()
	if err != nil {
		return err
	}

	installed, err := installPatch(zipPath, gameRoot)
	if err != nil {
		return err
	}
	fmt.Printf("已写入 %d 个文件。\n", installed)

	return nil
}

func readSteamPath() (string, error) {
	value, err := readCurrentUserStringValue(regSubKey, regValueName)
	if err != nil {
		return "", fmt.Errorf("读取注册表 HKCU\\%s\\%s 失败: %w", regSubKey, regValueName, err)
	}

	steamPath := filepath.Clean(strings.TrimSpace(value))
	if steamPath == "." || steamPath == "" {
		return "", errors.New("注册表中的 SteamPath 为空")
	}
	return steamPath, nil
}

func steamLibraries(steamPath string) ([]string, error) {
	vdfPath := filepath.Join(steamPath, "steamapps", libraryVDFName)
	data, err := os.ReadFile(vdfPath)
	if err != nil {
		return nil, fmt.Errorf("读取 Steam 库配置失败 %s: %w", vdfPath, err)
	}

	seen := make(map[string]bool)
	var libraries []string
	addLibrary := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		cleaned := filepath.Clean(value)
		key := strings.ToLower(cleaned)
		if seen[key] {
			return
		}
		seen[key] = true
		libraries = append(libraries, cleaned)
	}

	addLibrary(steamPath)

	paths, err := parseVDFPathValues(string(data))
	if err != nil {
		return nil, fmt.Errorf("解析 Steam 库配置失败 %s: %w", vdfPath, err)
	}
	for _, library := range paths {
		addLibrary(library)
	}

	if len(libraries) == 0 {
		return nil, errors.New("没有找到任何 Steam 库路径")
	}
	return libraries, nil
}

func parseVDFPathValues(input string) ([]string, error) {
	parser := vdfParser{data: input}
	var values []string

	for {
		token, ok, err := parser.nextString()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if !strings.EqualFold(token, "path") {
			continue
		}

		value, ok, err := parser.nextString()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("path 字段缺少值")
		}
		values = append(values, value)
	}

	return values, nil
}

type vdfParser struct {
	data string
	pos  int
}

func (p *vdfParser) nextString() (string, bool, error) {
	for p.pos < len(p.data) {
		if p.data[p.pos] == '"' {
			return p.readQuotedString()
		}
		p.pos++
	}
	return "", false, nil
}

func (p *vdfParser) readQuotedString() (string, bool, error) {
	p.pos++
	var builder strings.Builder

	for p.pos < len(p.data) {
		ch := p.data[p.pos]
		p.pos++

		if ch == '"' {
			return builder.String(), true, nil
		}
		if ch != '\\' {
			builder.WriteByte(ch)
			continue
		}
		if p.pos >= len(p.data) {
			return "", false, errors.New("VDF 字符串以反斜杠结尾")
		}

		escaped := p.data[p.pos]
		p.pos++
		switch escaped {
		case '\\', '"':
			builder.WriteByte(escaped)
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		default:
			builder.WriteByte('\\')
			builder.WriteByte(escaped)
		}
	}

	return "", false, errors.New("VDF 字符串缺少结束引号")
}

func findGameRoot(libraries []string) (string, error) {
	for _, library := range libraries {
		candidate := filepath.Join(library, "steamapps", "common", gameDirName)
		exePath := filepath.Join(candidate, gameExeName)
		info, err := os.Stat(exePath)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "检查游戏路径失败，已跳过 %s: %v\n", exePath, err)
		}
	}
	return "", fmt.Errorf("未找到游戏，请确认已安装：steamapps\\common\\%s\\%s", gameDirName, gameExeName)
}

func findPatchZip() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取当前程序路径失败: %w", err)
	}

	zipPath := filepath.Join(filepath.Dir(exePath), patchZipName)
	info, err := os.Stat(zipPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("未找到汉化包，请把 %s 放在本程序同目录下: %s", patchZipName, zipPath)
		}
		return "", fmt.Errorf("检查汉化包失败 %s: %w", zipPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("汉化包路径是目录，不是 ZIP 文件: %s", zipPath)
	}

	fmt.Printf("汉化包: %s\n", zipPath)
	return zipPath, nil
}

func installPatch(zipPath, gameRoot string) (int, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, fmt.Errorf("打开 ZIP 失败: %w", err)
	}
	defer reader.Close()

	prefix, err := findLogicalRootPrefix(reader.File)
	if err != nil {
		return 0, err
	}
	if prefix == "" {
		fmt.Println("ZIP 根目录: <ZIP 根>")
	} else {
		fmt.Printf("ZIP 根目录: %s\n", prefix)
	}

	written := 0
	for _, file := range reader.File {
		relative, ok, err := stripLogicalRoot(file.Name, prefix)
		if err != nil {
			return written, err
		}
		if !ok || relative == "" {
			continue
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(filepath.Join(gameRoot, filepath.FromSlash(relative)), 0o755); err != nil {
				return written, fmt.Errorf("创建目录失败 %s: %w", relative, err)
			}
			continue
		}

		target, err := safeJoin(gameRoot, relative)
		if err != nil {
			return written, err
		}
		if err := extractFile(file, target); err != nil {
			return written, err
		}
		written++
	}

	if written == 0 {
		return 0, errors.New("ZIP 中没有可写入的文件")
	}
	return written, nil
}

func findLogicalRootPrefix(files []*zip.File) (string, error) {
	for _, file := range files {
		name := strings.TrimLeft(strings.ReplaceAll(file.Name, "\\", "/"), "/")
		parts := strings.Split(name, "/")
		for i, part := range parts {
			if strings.EqualFold(part, "BepInEx") && (file.FileInfo().IsDir() || i < len(parts)-1) {
				return strings.Join(parts[:i], "/") + optionalSlash(i), nil
			}
		}
	}
	return "", errors.New("ZIP 内未找到名为 BepInEx 的文件夹")
}

func optionalSlash(count int) string {
	if count == 0 {
		return ""
	}
	return "/"
}

func stripLogicalRoot(name, prefix string) (string, bool, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	normalized = strings.TrimLeft(normalized, "/")

	if prefix != "" {
		if !strings.HasPrefix(normalized, prefix) {
			return "", false, nil
		}
		normalized = strings.TrimPrefix(normalized, prefix)
	}

	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return "", true, nil
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || path.IsAbs(cleaned) {
		return "", false, fmt.Errorf("ZIP 内存在不安全路径: %s", name)
	}
	return cleaned, true, nil
}

func safeJoin(root, relative string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(relative))
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析游戏目录失败: %w", err)
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("解析目标路径失败 %s: %w", target, err)
	}

	rootWithSep := strings.TrimRight(cleanRoot, `\/`) + string(os.PathSeparator)
	if cleanTarget != cleanRoot && !strings.HasPrefix(strings.ToLower(cleanTarget), strings.ToLower(rootWithSep)) {
		return "", fmt.Errorf("ZIP 内路径试图写出游戏目录: %s", relative)
	}
	return cleanTarget, nil
}

func extractFile(file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("创建目录失败 %s: %w", filepath.Dir(target), err)
	}

	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("读取 ZIP 文件失败 %s: %w", file.Name, err)
	}
	defer src.Close()

	perm := file.Mode().Perm()
	if perm == 0 {
		perm = 0o666
	}

	dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Chmod(target, 0o666)
		dst, err = os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	}
	if err != nil {
		return fmt.Errorf("写入文件失败 %s: %w", target, err)
	}

	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return fmt.Errorf("写入文件失败 %s: %w", target, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭文件失败 %s: %w", target, closeErr)
	}

	modTime := file.Modified
	if modTime.IsZero() {
		modTime = time.Now()
	}
	_ = os.Chtimes(target, modTime, modTime)
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	waitBeforeExit()
	os.Exit(1)
}

func waitBeforeExit() {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}

	fmt.Print("\n按 Enter 退出...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

const (
	hkeyCurrentUser = 0x80000001
	keyRead         = 0x20019
)

var (
	advapi32              = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyExW     = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW  = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey       = advapi32.NewProc("RegCloseKey")
	errRegistryValueEmpty = errors.New("注册表值为空")
)

func readCurrentUserStringValue(subKey, valueName string) (string, error) {
	subKeyPtr, err := syscall.UTF16PtrFromString(subKey)
	if err != nil {
		return "", err
	}

	var key syscall.Handle
	ret, _, _ := procRegOpenKeyExW.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(subKeyPtr)),
		0,
		keyRead,
		uintptr(unsafe.Pointer(&key)),
	)
	if ret != 0 {
		return "", syscall.Errno(ret)
	}
	defer procRegCloseKey.Call(uintptr(key))

	valueNamePtr, err := syscall.UTF16PtrFromString(valueName)
	if err != nil {
		return "", err
	}

	var valueType uint32
	var size uint32
	ret, _, _ = procRegQueryValueExW.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(valueNamePtr)),
		0,
		uintptr(unsafe.Pointer(&valueType)),
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 {
		return "", syscall.Errno(ret)
	}
	if size == 0 {
		return "", errRegistryValueEmpty
	}

	buffer := make([]uint16, (size+1)/2)
	ret, _, _ = procRegQueryValueExW.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(valueNamePtr)),
		0,
		uintptr(unsafe.Pointer(&valueType)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 {
		return "", syscall.Errno(ret)
	}
	if valueType != 1 && valueType != 2 {
		return "", fmt.Errorf("注册表值类型不是字符串: %d", valueType)
	}

	if len(buffer) > 0 && buffer[len(buffer)-1] == 0 {
		buffer = buffer[:len(buffer)-1]
	}
	return syscall.UTF16ToString(buffer), nil
}
