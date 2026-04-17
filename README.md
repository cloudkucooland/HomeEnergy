This environment polls my TP-Link kasa devices, my Enphase Envoy and my Daikin OneTouch thermostat in an effort to optimize my electric usage.

My goal is getting to a $0 bill for the whole year.

fill in dot.env wiht the proper values
rename dot.env to .env

Collect your various JWT and API tokens/keys from all the providers... the Daikin one is a pain

```
sudo docker compose up -d
```

Point your browser at https://<your host IP>:3000/ to see stats (username and password set in your .env)

I'll add more tools as I build them.
