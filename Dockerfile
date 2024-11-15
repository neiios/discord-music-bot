FROM oven/bun

RUN apt update && \
    apt install --no-install-recommends -y python3 python3-pip ffmpeg && \
    rm -rf /var/lib/apt/lists/*

RUN python3 -m pip install -U --pre "yt-dlp[nightly]"

COPY . .
RUN bun install

CMD ["bun", "run", "main.ts"]
