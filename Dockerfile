FROM httpd

RUN apt-get update && \
    apt-get -y install cron git build-essential python3 cmake pip && pip install conan && \
    apt-get clean autoclean && \
    apt-get autoremove --yes && \
    rm -rf /var/lib/{apt,dpkg,cache,log}/

RUN sed -i \
	-e '/#LoadModule cgid_module modules\/mod_cgid.so/s/#//g' \
	-e '/#LoadModule cgi_module modules\/mod_cgi.so/s/#//g' \
	-e '/CustomLog \/proc\/self\/fd\/1 common/s/^\s*/#/g' \
	conf/httpd.conf && \
  echo 'Include conf/extra/httpd-cgi-extra.conf' >> /usr/local/apache2/conf/httpd.conf && \
  rm -rf /usr/local/apache2/cgi-bin/

WORKDIR /

COPY ./usr/local/bin/ /usr/local/bin/
COPY ./usr/lib/cgi-bin/ /usr/local/apache2/cgi-bin/
COPY ./etc/ /etc/
COPY ./apache2-conf-extra/ /usr/local/apache2/conf/extra/
COPY ./var/ /var/
COPY ./opt/ /opt/

COPY start_all.sh /

RUN chown www-data:www-data /var/lib/redeploy

CMD ["/start_all.sh"]
