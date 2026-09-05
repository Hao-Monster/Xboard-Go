# Third-party notices

## Xboard App Clash rules template

`internal/subscription/assets/legacy_app_clash.yaml` is derived from the Xboard
project's `resources/rules/app.clash.yaml` to preserve client configuration
compatibility.

MIT License

Copyright (c) 2019 Tokumeikoi

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## ip2region Go binding and pinned IPv4 data

The Telegram ticket notification location resolver uses the Go binding from
`lionsoul2014/ip2region` v3.17.0 (commit
`cd40e3a1d532d645697999d646cf0e10481cef33`). The packaged image contains the
IPv4 XDB distributed by `zoujingli/ip2region` v2.0.8 (commit
`a031c359620c22889fac7b998409fdcdef76a69c`) so location text remains compatible
with the captured Xboard runtime. The data is pinned by SHA-256 and is not
stored in this repository.

Copyright (c) 2015 Lionsoul <chenxin619315@gmail.com>

The upstream project is dual-licensed under Apache-2.0 or MIT. This project
uses it under Apache-2.0; the full Apache License 2.0 text is provided in this
repository's `LICENSE` file. The `zoujingli/ip2region` package declares the
included XDB under Apache-2.0.

## NGINX and the NGINX unprivileged container image

The split-runtime frontend and same-origin gateway images use the official
NGINX unprivileged container image. The container packaging is licensed under
Apache-2.0. NGINX itself is distributed under the following two-clause BSD-like
license:

Copyright (C) Igor Sysoev

Copyright (C) Nginx, Inc.

All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice,
   this list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE AUTHOR AND CONTRIBUTORS ``AS IS'' AND ANY
EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE AUTHOR OR CONTRIBUTORS BE LIABLE FOR ANY
DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES
(INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES;
LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON
ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
