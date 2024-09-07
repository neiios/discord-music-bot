FROM oven/bun

RUN apt update && \
    apt install --no-install-recommends -y python3 python3-pip ffmpeg && \
    rm -rf /var/lib/apt/lists/*

RUN python3 -m pip install -U --pre "yt-dlp[default]"

RUN python3 -m pip install -U https://github.com/coletdjnz/yt-dlp-youtube-oauth2/archive/refs/heads/master.zip

COPY . .
RUN bun install

CMD ["bun", "run", "main.ts"]
