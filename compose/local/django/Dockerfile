# =============================================================================
# music-tag-web 本地构建 - Django Dockerfile
# --------------------------------------------------------------------
# Python 3.12 + Debian bookworm 基底 (stable LTS 友好)
# - apt 中 default-libmysqlclient-dev 在 bookworm 中仍以 virtual
#   libmariadb-dev-compat / default-libmysqlclient-dev 提供
# - nodejs 需 pin 到 18.x 以兼容本项目 vue-cli 2 + webpack 3 + node-sass@8
# =============================================================================

FROM python:3.12-slim-bookworm as python

# ---------- Python build stage: 编译 wheel ----------
FROM python as python-build-stage

ARG BUILD_ENVIRONMENT=local
# 编译期需要的包
RUN apt-get update && apt-get install --no-install-recommends -y \
  build-essential \
  libpq-dev \
  default-libmysqlclient-dev \
  libffi-dev \
  libjpeg-dev \
  libxml2 \
  libxslt1-dev \
  pkg-config \
  && rm -rf /var/lib/apt/lists/*

COPY ./requirements .
# 把依赖打成 wheel 缓存起来 (后续 stage 直接装 wheel，跳过编译)
RUN pip wheel --wheel-dir /usr/src/app/wheels \
  -r ${BUILD_ENVIRONMENT}.txt -i https://pypi.tuna.tsinghua.edu.cn/simple

# ---------- Python run stage: 最终运行时镜像 ----------
FROM python as python-run-stage

LABEL title="Music Tag Web | 音乐标签网页版"
LABEL description="『音乐标签』Web版是一款可以编辑歌曲的标题，专辑，艺术家，歌词，封面等信息的应用程序"
LABEL authors="xhongc"

ARG BUILD_ENVIRONMENT=local
ARG APP_HOME=/app

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    BUILD_ENV=${BUILD_ENVIRONMENT} \
    \
    # nodejs 18.x (bookworm 默认 nodejs 现在是 19+，对旧 webpack 3 不友好)
    NODE_MAJOR=18

WORKDIR ${APP_HOME}

# 运行时需要的 apt 包
RUN apt-get update && apt-get install --no-install-recommends -y \
  libpq-dev \
  gettext \
  default-libmysqlclient-dev \
  curl \
  gnupg \
  ca-certificates \
  && mkdir -p /etc/apt/keyrings \
  && curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
  && apt-get install --no-install-recommends -y nodejs \
  && apt-get purge -y --auto-remove -o APT::AutoRemove::RecommendsImportant=false \
  && rm -rf /var/lib/apt/lists/*

# 安装编译好的 wheel
COPY --from=python-build-stage /usr/src/app/wheels  /wheels/
RUN pip install --no-cache-dir --no-index --find-links=/wheels/ /wheels/* \
    && rm -rf /wheels/

COPY ./compose/local/django/start /start
RUN sed -i 's/\r$//g' /start && chmod +x /start

COPY ./compose/local/django/celery/worker/start /start-celeryworker
RUN sed -i 's/\r$//g' /start-celeryworker && chmod +x /start-celeryworker

COPY ./compose/local/django/celery/beat/start /start-celerybeat
RUN sed -i 's/\r$//g' /start-celerybeat && chmod +x /start-celerybeat

COPY . ${APP_HOME}

CMD ["/start"]
