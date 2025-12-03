#!/bin/bash

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

echo -e "${GREEN}=== MP3转AMR工具 一键部署脚本 (本地离线版) ===${NC}"

# 检查是否为 root 用户
if [ "$EUID" -ne 0 ]; then 
  echo -e "${RED}请使用 root 权限运行此脚本 (sudo bash install.sh)${NC}"
  exit 1
fi

# 1. 安装基本工具
echo -e "${GREEN}[1/6] 安装基本工具 (Golang, wget, xz)...${NC}"
export DEBIAN_FRONTEND=noninteractive

# 尝试更换为阿里云镜像源（防止 apt update 卡住）
if grep -q "debian" /etc/os-release; then
    if [ ! -f "/etc/apt/sources.list.bak" ]; then
        cp /etc/apt/sources.list /etc/apt/sources.list.bak
        sed -i 's/deb.debian.org/mirrors.aliyun.com/g' /etc/apt/sources.list
        sed -i 's/security.debian.org/mirrors.aliyun.com/g' /etc/apt/sources.list
    fi
fi

apt-get update
apt-get install -y golang git wget xz-utils

if ! command -v go &> /dev/null; then
    echo -e "${RED}Go 安装失败，请手动运行 'apt-get install -y golang' 尝试${NC}"
    exit 1
fi

# 2. 安装 FFmpeg (优先使用本地压缩包)
echo -e "${GREEN}[2/6] 正在安装 FFmpeg...${NC}"
INSTALL_DIR="/opt/mp3toamr"
mkdir -p $INSTALL_DIR

LOCAL_ARCHIVE="ffmpeg-master-latest-linux64-gpl.tar.xz"

# 检查是否已经存在手动上传的 ffmpeg 二进制文件
if [ -f "./ffmpeg" ]; then
    echo -e "${GREEN}发现已解压的 ffmpeg 文件，直接使用...${NC}"
    cp ./ffmpeg $INSTALL_DIR/ffmpeg
    chmod +x $INSTALL_DIR/ffmpeg

# 检查是否存在本地压缩包 (这是你现在的情况)
elif [ -f "./$LOCAL_ARCHIVE" ]; then
    echo -e "${GREEN}发现本地压缩包 $LOCAL_ARCHIVE，正在解压...${NC}"
    tar xf "$LOCAL_ARCHIVE"
    
    # 查找解压出来的 ffmpeg 可执行文件
    FFMPEG_BIN=$(find . -type f -name "ffmpeg" | head -n 1)
    
    if [ -n "$FFMPEG_BIN" ]; then
        cp "$FFMPEG_BIN" $INSTALL_DIR/ffmpeg
        chmod +x $INSTALL_DIR/ffmpeg
        echo -e "${GREEN}静态 FFmpeg 安装成功！${NC}"
        # 清理解压出来的文件夹，保留压缩包
        rm -rf ffmpeg-master-*
    else
        echo -e "${RED}解压后未找到 ffmpeg 文件，请检查压缩包内容。${NC}"
        exit 1
    fi

else
    # 本地没有文件，尝试下载 (作为最后的备选)
    echo -e "${YELLOW}未找到本地文件，尝试在线下载...${NC}"
    DOWNLOAD_URL="https://mirror.ghproxy.com/https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.tar.xz"
    
    if wget --show-progress -O ffmpeg.tar.xz "$DOWNLOAD_URL"; then
        tar xf ffmpeg.tar.xz
        FFMPEG_BIN=$(find . -type f -name "ffmpeg" | head -n 1)
        if [ -n "$FFMPEG_BIN" ]; then
            cp "$FFMPEG_BIN" $INSTALL_DIR/ffmpeg
            chmod +x $INSTALL_DIR/ffmpeg
        fi
        rm -rf ffmpeg.tar.xz ffmpeg-master-*
    else
        echo -e "${RED}在线下载失败！请上传 $LOCAL_ARCHIVE 到当前目录。${NC}"
        exit 1
    fi
fi

# 验证 ffmpeg 是否存在
if [ ! -f "$INSTALL_DIR/ffmpeg" ]; then
    echo -e "${RED}错误: FFmpeg 安装失败。${NC}"
    exit 1
fi

# 3. 编译 Go 项目
echo -e "${GREEN}[3/6] 正在编译项目...${NC}"

export GO111MODULE=on
export GOPROXY=https://goproxy.cn,direct

# 确保 go.mod 存在
if [ ! -f "go.mod" ]; then
    go mod init mp3toamr
fi

echo "下载依赖..."
go mod tidy

echo "编译中..."
go build -o mp3toamr-server main.go

if [ ! -f "./mp3toamr-server" ]; then
    echo -e "${RED}编译失败！${NC}"
    exit 1
fi

# 4. 部署文件
echo -e "${GREEN}[4/6] 部署程序文件...${NC}"
# 停止旧服务
systemctl stop mp3toamr 2>/dev/null

cp mp3toamr-server $INSTALL_DIR/
# 确保 ffmpeg 也在目录下
if [ -f "./ffmpeg" ]; then
    cp ./ffmpeg $INSTALL_DIR/
fi

# 创建临时文件夹
mkdir -p $INSTALL_DIR/temp
chmod 777 $INSTALL_DIR/temp

# 5. 创建 Systemd 服务
echo -e "${GREEN}[5/6] 创建系统服务...${NC}"
cat > /etc/systemd/system/mp3toamr.service <<EOF
[Unit]
Description=MP3 to AMR Converter Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/mp3toamr-server
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

# 6. 启动服务
echo -e "${GREEN}[6/6] 启动服务...${NC}"
systemctl daemon-reload
systemctl enable mp3toamr
systemctl restart mp3toamr

# 获取本机IP
IP=$(hostname -I | awk '{print $1}')

echo -e "${GREEN}==========================================${NC}"
echo -e "${GREEN}部署成功！${NC}"
echo -e "服务状态：$(systemctl is-active mp3toamr)"
echo -e "访问地址: http://$IP:8080"
echo -e "${GREEN}==========================================${NC}"
