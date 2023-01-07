#!/usr/bin/env bash

set -eu

# clone or pull git
if [[ -d build_dir ]]; then
  cd build_dir
  git pull
  cd ..
else
  git clone "$(./get-git-url.sh)" build_dir
fi

# prepare env
export PATH=/usr/local/apache2/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export HOME=/root

# execute build steps
cd build_dir
rm -rf build
mkdir -p build && cd build
conan install ..
cmake ..
cmake --build .
ctest
cmake --install .
cd ../..
