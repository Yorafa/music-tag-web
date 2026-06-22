from django.conf import settings
from django.contrib import admin
from django.urls import path, include, re_path
# Django 4.0+ 移除了 django.views.static.serve；改用 django.contrib.staticfiles.views.serve
from django.contrib.staticfiles.views import serve as static_serve
from music_site.views import index
from applications.task.urls import router as task_router
from applications.user.urls import router as user_router
from applications.subsonic.urls import router as subsonic_router
# simplejwt 取代了 djangorestframework-jwt (后者 2019 后未维护)
from rest_framework_simplejwt.views import TokenObtainPairView, TokenRefreshView, TokenVerifyView

urlpatterns = [
    path('admin/', admin.site.urls),
    path('', index),
    re_path(r"^api/", include(task_router.urls)),
    re_path(r"^rest/", include(subsonic_router.urls)),
    re_path(r"^user/", include(user_router.urls)),
    # simplejwt 三个标准端点 (POST)
    #   /api/token/         -> {access, refresh}
    #   /api/token/refresh/ -> {access}
    #   /api/token/verify/  -> {} (validates token)
    path('api/token/', TokenObtainPairView.as_view(), name='token_obtain_pair'),
    path('api/token/refresh/', TokenRefreshView.as_view(), name='token_refresh'),
    path('api/token/verify/', TokenVerifyView.as_view(), name='token_verify'),
    # 静态 / 媒体: 都是 Django 4.0+ 的标准写法
    re_path(r'^static/(?P<path>.*)$', static_serve,
            {'document_root': settings.STATIC_ROOT}, name='static'),
    re_path(r'^media/(?P<path>.*)$', static_serve, {'document_root': settings.MEDIA_ROOT}),
] 
