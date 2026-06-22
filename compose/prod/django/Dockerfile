# =============================================================================
# music-tag-web 生产构建 - Django Dockerfile
# --------------------------------------------------------------------
# 与 local 同体镜像，仅 BUILD_ENVIRONMENT=prod + 末尾多执行
# python setup_in_docker.py 用于初始化 admin 账号等。
# =============================================================================

FROM python:3.12-slim-bookworm as python

# ---------- build stage ----------
FROM python as python-build-stage

ARG BUILD_ENVIRONMENT=prod

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
RUN pip wheel --wheel-dir /usr/src/app/wheels \
  -r ${BUILD_ENVIRONMENT}.txt -i https://pypi.tuna.tsinghua.edu.cn/simple

# ---------- run stage ----------
FROM python as python-run-stage

LABEL title="Music Tag Web | 音乐标签网页版"
LABEL description="『音乐标签』Web版是一款可以编辑歌曲的标题，专辑，艺术家，歌词，封面等信息的应用程序"
LABEL authors="xhongc"

ARG BUILD_ENVIRONMENT=prod
ARG APP_HOME=/app

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    BUILD_ENV=${BUILD_ENVIRONMENT} \
    NODE_MAJOR=18

WORKDIR ${APP_HOME}

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

COPY --from=python-build-stage /usr/src/app/wheels  /wheels/
RUN pip install --no-cache-dir --no-index --find-links=/wheels/ /wheels/* \
    && rm -rf /wheels/

COPY ./compose/prod/django/start /start
RUN sed -i 's/\r$//g' /start && chmod +x /start

COPY ./compose/prod/django/celery/worker/start /start-celeryworker
RUN sed -i 's/\r$//g' /start-celeryworker && chmod +x /start-celeryworker

COPY ./compose/prod/django/celery/beat/start /start-celerybeat
RUN sed -i 's/\r$//g' /start-celerybeat && chmod +x /start-celerybeat

COPY . ${APP_HOME}
RUN python setup_in_docker.py
CMD ["/start"]
