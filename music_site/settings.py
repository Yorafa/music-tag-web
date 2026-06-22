from pathlib import Path
import sys
import os
import datetime

# lib文件夹中手动导入的第三方库
BASE_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(1, os.path.join(os.getcwd(), 'lib'))

SECRET_KEY = 'django-insecure-u5_r=pekio0@zt!y(kgbufuosb9mddu8*qeejkzj@=7uyvb392'

DEBUG = os.getenv('DJANGO_DEBUG', '1') == '1'

ALLOWED_HOSTS = ["*"]
CORS_ALLOW_CREDENTIALS = True
CSRF_COOKIE_NAME = "music_site_csrftoken"
CORS_ORIGIN_WHITELIST = [
    "http://127.0.0.1:8080"
]

INSTALLED_APPS = [
    "corsheaders",
    'django.contrib.admin',
    'django.contrib.auth',
    'django.contrib.contenttypes',
    'django.contrib.sessions',
    'django.contrib.messages',
    'django.contrib.staticfiles',
    "rest_framework",
    "applications.task",
    "applications.user",
    "applications.music",
    "applications.subsonic",
    # "django_extensions",

]

MIDDLEWARE = [
    "corsheaders.middleware.CorsMiddleware",
    'django.middleware.security.SecurityMiddleware',
    'django.contrib.sessions.middleware.SessionMiddleware',
    'django.middleware.common.CommonMiddleware',
    'django.middleware.csrf.CsrfViewMiddleware',
    'django.contrib.auth.middleware.AuthenticationMiddleware',
    'django.contrib.messages.middleware.MessageMiddleware',
    "component.drf.middleware.AppExceptionMiddleware",
    'django.middleware.clickjacking.XFrameOptionsMiddleware',
]

ROOT_URLCONF = 'music_site.urls'

TEMPLATES = [
    {
        'BACKEND': 'django.template.backends.django.DjangoTemplates',
        'DIRS': [os.path.join(BASE_DIR, "static", "dist")],
        'APP_DIRS': True,
        'OPTIONS': {
            'context_processors': [
                'django.template.context_processors.debug',
                'django.template.context_processors.request',
                'django.contrib.auth.context_processors.auth',
                'django.contrib.messages.context_processors.messages',
            ],
        },
    },
]

WSGI_APPLICATION = 'music_site.wsgi.application'
TIME_ZONE = "Asia/Shanghai"
LANGUAGE_CODE = "zh-hans"
if os.getenv("dockerrun", "no") == "yes":
    REDIS_HOST = "redis"
    MYSQL_HOST = "db"
else:
    REDIS_HOST = "127.0.0.1"
    MYSQL_HOST = "127.0.0.1"

# Database
DATABASES = {
    'default': {
        'ENGINE': 'django.db.backends.sqlite3',
        'NAME': os.path.join(BASE_DIR, 'db.sqlite3'),
    }
}
# DATABASES = {
#     "default": {
#         "ENGINE": "django.db.backends.mysql",
#         "NAME": 'music3',  # noqa
#         "USER": "root",
#         "PASSWORD": "123456",
#         "HOST": MYSQL_HOST,
#         "PORT": "3306",
#     },
# }
# Password validation
# https://docs.djangoproject.com/en/3.2/ref/settings/#auth-password-validators

AUTH_PASSWORD_VALIDATORS = [
    {
        'NAME': 'django.contrib.auth.password_validation.UserAttributeSimilarityValidator',
    },
    {
        'NAME': 'django.contrib.auth.password_validation.MinimumLengthValidator',
    },
    {
        'NAME': 'django.contrib.auth.password_validation.CommonPasswordValidator',
    },
    {
        'NAME': 'django.contrib.auth.password_validation.NumericPasswordValidator',
    },
]

USE_I18N = True

# USE_L10N 在 Django 4.0+ 已由 USE_I18N=True 隐式启用, Django 5.0 会移除该设置。
USE_TZ = False

STATIC_URL = '/static/'
STATIC_ROOT = os.path.join(BASE_DIR, 'collected_static')
STATICFILES_DIRS = [os.path.join(BASE_DIR, "static")]  # 让 runserver 的 staticfiles finder 能搜到 static/dist/ 下的 Vue 构建产物
DEFAULT_AUTO_FIELD = 'django.db.models.BigAutoField'
IS_USE_CELERY = False

if IS_USE_CELERY:
    BROKER_URL = f"redis://{REDIS_HOST}:6379/1"
    CELERY_TIMEZONE = 'Asia/Shanghai'
    INSTALLED_APPS += ("django_celery_beat", "django_celery_results")
    CELERY_ENABLE_UTC = False
    ENABLE_UTC = False
    DJANGO_CELERY_BEAT_TZ_AWARE = False

    CELERY_TASK_SERIALIZER = "pickle"
    CELERY_ACCEPT_CONTENT = ['pickle', ]
    CELERYBEAT_SCHEDULER = "django_celery_beat.schedulers.DatabaseScheduler"

REST_FRAMEWORK = {
    "EXCEPTION_HANDLER": "component.drf.generics.exception_handler",
    "DEFAULT_PERMISSION_CLASSES": ("rest_framework.permissions.IsAuthenticated",),
    "DEFAULT_PAGINATION_CLASS": "component.drf.pagination.CustomPageNumberPagination",
    "PAGE_SIZE": 10,
    "TEST_REQUEST_DEFAULT_FORMAT": "json",
    # simplejwt 取代 djangorestframework-jwt (后者 2019 后未维护)
    # 只保留 JWT 认证；去掉 SessionAuthentication 避免 CSRF token 冲突
    # (前端只通过 /api/token/ 拿 JWT 存 cookie，不走 Django session)
    'DEFAULT_AUTHENTICATION_CLASSES': [
        'rest_framework_simplejwt.authentication.JWTAuthentication',
    ],
    "DEFAULT_FILTER_BACKENDS": (
        "django_filters.rest_framework.DjangoFilterBackend",
        "rest_framework.filters.OrderingFilter",
    ),
    "DATETIME_FORMAT": "%Y-%m-%d %H:%M:%S",
    "NON_FIELD_ERRORS_KEY": "params_error",
}

# simplejwt 配置
# - 保留原有 'JWT' 前缀与 7 天期限以便前端额外改动最小
# - AUTH_COOKIE 名为 'AUTHORIZATION' 以对齐前端 axiosconfig.js 读取的 cookie 名
# - TODO: 前端改用 localStorage 存储 token 后，将 AUTH_COOKIE_HTTP_ONLY 改为 True（防 XSS）
SIMPLE_JWT = {
    'ACCESS_TOKEN_LIFETIME': datetime.timedelta(days=7),
    'REFRESH_TOKEN_LIFETIME': datetime.timedelta(days=7),
    'ROTATE_REFRESH_TOKENS': False,
    'BLACKLIST_AFTER_ROTATION': False,
    # DRF 端点赋 Authorization: JWT <token>，同时兼容 Bearer 和前端小写 jwt
    'AUTH_HEADER_TYPES': ('JWT', 'jwt', 'Bearer', 'bearer'),
    # 为了让前端 axiosconfig.js 读到的 'AUTHORIZATION' cookie 仍然生效:
    'AUTH_COOKIE': 'AUTHORIZATION',
    'AUTH_COOKIE_SECURE': False,
    'AUTH_COOKIE_HTTP_ONLY': False,
    'AUTH_COOKIE_SAMESITE': 'Lax',
}
BASE_URL = "https://music.163.com/"
REVERSE_PROXY_TYPE = "nginx"
MEDIA_URL = '/media/'
MEDIA_ROOT = os.path.join(BASE_DIR, "media")
SUBSONIC_DEFAULT_TRANSCODING_FORMAT = "mp3"
SITE_LOGIN = os.getenv("SITE_LOGIN", "true")
try:
    from local_settings import *  # noqa
except ImportError:
    pass
