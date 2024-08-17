FROM oven/bun

RUN apt update && \
    apt install --no-install-recommends -y python3 python3-pip ffmpeg && \
    rm -rf /var/lib/apt/lists/*

RUN pip3 install yt-dlp

COPY . .
RUN bun install

ENTRYPOINT ["bun", "run", "main.ts"]
