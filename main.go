package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

// 全局变量存储 ffmpeg 路径
var ffmpegPath string

// 全局变量存储下载路径前缀
var downloadDir = "downloads"

func init() {
	// 1. 优先查找当前目录下的 ffmpeg
	cwd, _ := os.Getwd()
	localFFmpeg := filepath.Join(cwd, "ffmpeg")
	if _, err := os.Stat(localFFmpeg); err == nil {
		ffmpegPath = localFFmpeg
		fmt.Println("使用本地 ffmpeg:", ffmpegPath)
	} else {
		// 2. 否则查找系统路径
		systemFFmpeg, err := exec.LookPath("ffmpeg")
		if err == nil {
			ffmpegPath = systemFFmpeg
			fmt.Println("使用系统 ffmpeg:", ffmpegPath)
		} else {
			log.Fatal("错误: 未找到 ffmpeg。请确保目录下有 ffmpeg 可执行文件或已安装在系统中")
		}
	}

	// 创建下载目录
	if _, err := os.Stat(downloadDir); os.IsNotExist(err) {
		os.Mkdir(downloadDir, 0755)
	}
	
	// 启动清理任务
	go cleanUpTask()
}

func main() {
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/convert-url", handleUrlConvert)
	http.HandleFunc("/download/", handleDownload) // 新增下载路由

	port := "8080"
	fmt.Printf("服务器启动在 http://0.0.0.0:%s\n", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("无法启动服务器: ", err)
	}
}

// 定时清理旧文件 (1小时前的)
func cleanUpTask() {
	for {
		time.Sleep(10 * time.Minute)
		files, err := os.ReadDir(downloadDir)
		if err != nil {
			continue
		}
		now := time.Now()
		for _, file := range files {
			info, err := file.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) > 1*time.Hour {
				os.Remove(filepath.Join(downloadDir, file.Name()))
			}
		}
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// 核心转换逻辑：输入文件路径 -> 转换 -> 返回输出文件路径
func convertCore(inputPath string) (string, error) {
	outputName := inputPath + ".amr"
	
	// FFmpeg conversion command
	cmd := exec.Command(ffmpegPath, "-y", "-i", inputPath, "-ac", "1", "-ar", "8000", "-c:a", "libopencore_amrnb", outputName)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s\nOutput: %s", err, string(output))
	}
	return outputName, nil
}

// 修改后的辅助函数：保存文件并返回下载链接 JSON
// 参数增加 originalMp3Path: 如果不为空，则说明需要同时提供MP3下载
func serveFile(w http.ResponseWriter, amrPath string, downloadName string, originalMp3Path string) {
	// 1. 处理 AMR 文件
	convertedFile, err := os.Open(amrPath)
	if err != nil {
		http.Error(w, "Result file read error", http.StatusInternalServerError)
		return
	}
	defer convertedFile.Close()

	// 确保文件名以 .amr 结尾
	if !strings.HasSuffix(strings.ToLower(downloadName), ".amr") {
		downloadName += ".amr"
	}

	// 生成安全的 AMR 文件名
	safeName := sanitizeFilename(downloadName)
	if len(safeName) > 50 {
		safeName = safeName[:50] 
	}
	
	timestamp := time.Now().Unix()
	finalAmrName := fmt.Sprintf("%d_%s", timestamp, safeName)
	if !strings.HasSuffix(finalAmrName, ".amr") {
		finalAmrName += ".amr"
	}
	
	finalAmrPath := filepath.Join(downloadDir, finalAmrName)
	outFile, err := os.Create(finalAmrPath)
	if err != nil {
		http.Error(w, "Save file error", http.StatusInternalServerError)
		return
	}
	io.Copy(outFile, convertedFile) // 复制内容
	outFile.Close()

	// 2. 处理 MP3 文件 (如果有)
	var mp3Url string
	var mp3Name string
	
	if originalMp3Path != "" {
		mp3File, err := os.Open(originalMp3Path)
		if err == nil {
			defer mp3File.Close()
			
			// 生成对应的 MP3 文件名 (替换后缀)
			mp3Name = strings.TrimSuffix(downloadName, ".amr") + ".mp3"
			safeMp3Name := strings.TrimSuffix(safeName, ".amr") + ".mp3"
			
			finalMp3Name := fmt.Sprintf("%d_%s", timestamp, safeMp3Name)
			finalMp3Path := filepath.Join(downloadDir, finalMp3Name)
			
			outMp3, err := os.Create(finalMp3Path)
			if err == nil {
				io.Copy(outMp3, mp3File)
				outMp3.Close()
				mp3Url = "/download/" + finalMp3Name
			}
		}
	}

	// 返回 JSON
	w.Header().Set("Content-Type", "application/json")
	response := map[string]string{
		"status": "success",
		"url":    "/download/" + finalAmrName,
		"name":   downloadName,
	}
	
	if mp3Url != "" {
		response["mp3Url"] = mp3Url
		response["mp3Name"] = mp3Name
	}
	
	json.NewEncoder(w).Encode(response)
}

// 新增：处理文件下载
func handleDownload(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/download/")
	// 安全检查
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(downloadDir, filename)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	// 提取原始文件名（去掉前缀的时间戳）
	originalName := filename
	parts := strings.SplitN(filename, "_", 2)
	if len(parts) == 2 {
		originalName = parts[1]
	}

	// 强制下载头
	encodedName := url.PathEscape(originalName)
	
	// 动态判断 Content-Type
	contentType := "audio/amr"
	if strings.HasSuffix(strings.ToLower(filename), ".mp3") {
		contentType = "audio/mpeg"
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", originalName, encodedName))
	w.Header().Set("Content-Type", contentType)
	
	http.ServeFile(w, r, filePath)
}


// 辅助函数：请求外部文本API
func fetchAPI(urlStr string) string {
	client := &http.Client{}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// 辅助函数：清洗文件名
func sanitizeFilename(name string) string {
	// 替换掉非法字符
	invalidChars := regexp.MustCompile(`[\\/:*?"<>|]`)
	return invalidChars.ReplaceAllString(name, "_")
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit upload size to 50MB
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "File too big", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create temp directory
	tempDir := "temp"
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		os.Mkdir(tempDir, 0755)
	}

	tempInput, err := os.CreateTemp(tempDir, "upload-*.mp3")
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tempInput.Name())
	defer tempInput.Close()

	if _, err := io.Copy(tempInput, file); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Conversion
	outputPath, err := convertCore(tempInput.Name())
	if err != nil {
		log.Printf("FFmpeg error: %v", err)
		http.Error(w, fmt.Sprintf("FFmpeg Error: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.Remove(outputPath)

	// Serve (Upload 模式不需要保留 MP3，因为是用户上传的)
	filename := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	serveFile(w, outputPath, filename+".amr", "")
	
	log.Printf("Converted upload: %s", header.Filename)
}

func handleUrlConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	urlStr := r.FormValue("url")
	if urlStr == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	var filename string
	var isNetease bool // 标记是否为网易云

	// === 网易云音乐链接智能解析 ===
	if strings.Contains(urlStr, "music.163.com") {
		re := regexp.MustCompile(`[?&]id=(\d+)`)
		matches := re.FindStringSubmatch(urlStr)
		
		if len(matches) > 1 {
			neteaseID := matches[1]
			log.Printf("检测到网易云ID: %s", neteaseID)
			isNetease = true
			
			// 并行获取歌名和歌手名以加快速度
			var songName, artistName string
			var wg sync.WaitGroup
			wg.Add(2)
			
			go func() {
				defer wg.Done()
				songName = fetchAPI(fmt.Sprintf("https://v.iarc.top/?type=name&id=%s", neteaseID))
			}()
			
			go func() {
				defer wg.Done()
				artistName = fetchAPI(fmt.Sprintf("https://v.iarc.top/?type=artist&id=%s", neteaseID))
			}()
			
			wg.Wait()

			// 拼接文件名：歌名_歌手
			if songName != "" {
				if artistName != "" {
					filename = fmt.Sprintf("%s_%s", songName, artistName)
				} else {
					filename = songName
				}
				// 清洗文件名防止包含非法字符
				filename = sanitizeFilename(filename)
				log.Printf("从接口获取到文件名: %s", filename)
			}

			// 替换下载链接
			urlStr = fmt.Sprintf("https://v.iarc.top/?type=url&id=%s", neteaseID)
		}
	}

	// 如果不是网易云或者提取名字失败，回退到常规解析逻辑
	if filename == "" {
		parsedUrl, err := url.Parse(urlStr)
		if err == nil {
			path := parsedUrl.Path
			base := filepath.Base(path)
			if base != "." && base != "/" && base != "" {
				filename = strings.TrimSuffix(base, filepath.Ext(base))
			}
		}
	}
	
	// 最终保底
	if filename == "" {
		filename = "arcpi"
	}

	// 2. 下载远程文件
	client := &http.Client{}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to download file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("Download failed with status: %d", resp.StatusCode), http.StatusBadRequest)
		return
	}

	// Create temp directory
	tempDir := "temp"
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		os.Mkdir(tempDir, 0755)
	}

	tempInput, err := os.CreateTemp(tempDir, "url-*.mp3")
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	// 只有当不需要保留 MP3 时，我们才会在函数结束时删除临时文件
	// 如果需要保留，我们在 serveFile 复制完后再删除
	
	defer tempInput.Close()

	if _, err := io.Copy(tempInput, resp.Body); err != nil {
		http.Error(w, "Download save failed", http.StatusInternalServerError)
		os.Remove(tempInput.Name()) // 出错了，删掉
		return
	}

	// 3. 转换
	outputPath, err := convertCore(tempInput.Name())
	if err != nil {
		log.Printf("FFmpeg error: %v", err)
		http.Error(w, fmt.Sprintf("FFmpeg Error: %v", err), http.StatusInternalServerError)
		os.Remove(tempInput.Name()) // 出错了，删掉
		return
	}
	defer os.Remove(outputPath)

	// 4. 返回
	// 只有是网易云链接时，才传入 tempInput.Name() (原始MP3路径)，否则传空字符串
	mp3PathToSave := ""
	if isNetease {
		mp3PathToSave = tempInput.Name()
	}
	
	serveFile(w, outputPath, filename+".amr", mp3PathToSave)
	
	// serveFile 内部会打开文件进行复制，复制完后我们这里删除临时文件是安全的
	// 因为 serveFile 是同步调用的
	os.Remove(tempInput.Name())

	log.Printf("Converted URL: %s -> %s.amr", urlStr, filename)
}