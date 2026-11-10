// genres 生成 Windows 资源文件（.syso），供 go build 自动嵌入。
//
// 复刻 Wails CLI 打包时的资源注入：应用图标（RT_GROUP_ICON ID=3，运行期
// 窗口/任务栏图标按该 ID 加载）、DPI 感知清单与版本信息。缺了这一步，
// 纯 go build 产出的 exe 没有文件图标，任务栏也退回默认图标。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

const manifestXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly manifestVersion="1.0" xmlns="urn:schemas-microsoft-com:asm.v1" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
    <assemblyIdentity type="win32" name="com.wails.%s" version="%s.0" processorArchitecture="*"/>
    <dependency>
        <dependentAssembly>
            <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
        </dependentAssembly>
    </dependency>
    <asmv3:application>
        <asmv3:windowsSettings>
            <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
            <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">permonitorv2,permonitor</dpiAwareness>
        </asmv3:windowsSettings>
    </asmv3:application>
</assembly>`

func main() {
	iconPath := flag.String("icon", "", "icon.ico 路径")
	outPath := flag.String("out", "", "输出 .syso 路径")
	product := flag.String("product", "真果鉴", "产品名称")
	origin := flag.String("origin", "zhenguojian", "内部名/原始文件名基名")
	ver := flag.String("version", "2.0.0", "点分版本 x.y.z")
	flag.Parse()

	data, err := os.ReadFile(*iconPath)
	if err != nil {
		fail("读取图标失败", err)
	}
	ico, err := winres.LoadICO(bytes.NewReader(data))
	if err != nil {
		fail("解析 ICO 失败", err)
	}

	rs := winres.ResourceSet{}
	// 与 wails CLI 相同：组图标资源 ID 传 RT_ICON(=3)，运行期按 ID 3 取窗口图标。
	if err := rs.SetIcon(winres.RT_ICON, ico); err != nil {
		fail("写入图标资源失败", err)
	}

	xmlData, err := winres.AppManifestFromXML([]byte(fmt.Sprintf(manifestXML, *origin, numVersion(*ver))))
	if err != nil {
		fail("清单生成失败", err)
	}
	rs.SetManifest(xmlData)

	vi := version.Info{}
	vi.SetFileVersion(*ver)
	vi.SetProductVersion(*ver)
	_ = vi.Set(0x0804, "FileDescription", *product)
	_ = vi.Set(0x0804, "ProductName", *product)
	_ = vi.Set(0x0804, "OriginalFilename", *origin+".exe")
	_ = vi.Set(0x0804, "InternalName", *origin)
	_ = vi.Set(0x0804, "CompanyName", "zhenguojian")
	_ = vi.Set(0x0804, "LegalCopyright", "MIT")
	rs.SetVersionInfo(vi)

	dir := filepath.Dir(*outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("创建输出目录失败", err)
	}
	f, err := os.Create(*outPath)
	if err != nil {
		fail("创建 syso 失败", err)
	}
	defer f.Close()
	if err := rs.WriteObject(f, winres.ArchAMD64); err != nil {
		fail("写入 syso 失败", err)
	}
	fmt.Println("genres: wrote", *outPath)
}

func numVersion(v string) string {
	parts := strings.SplitN(v, "-", 2)[0]
	segs := strings.Split(parts, ".")
	for len(segs) < 3 {
		segs = append(segs, "0")
	}
	out := make([]string, 3)
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(segs[i])
		if err != nil {
			n = 0
		}
		out[i] = strconv.Itoa(n)
	}
	return strings.Join(out, ".")
}

func fail(msg string, err error) {
	fmt.Fprintf(os.Stderr, "genres: %s: %v\n", msg, err)
	os.Exit(1)
}
